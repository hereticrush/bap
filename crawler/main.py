# crawler/main.py
#
# Autonomous crawler that pulls content from RSS feeds, filters for quality and safety
# using Gemini (rejecting violence and mature content), enriches prompts, and saves
# them directly to SQLite.
#
# Copyright (C) 2026 hereticrush — Licensed under GPL-3.0

import os
import sys
import time
import json
import logging
import sqlite3
import xml.etree.ElementTree as ET
import requests
from bs4 import BeautifulSoup
from google import genai
from google.genai import types

# Setup logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    handlers=[logging.StreamHandler(sys.stdout)]
)

DB_PATH = os.environ.get("DB_PATH", "/app/data/bap.db")
CRAWLER_FEED_URLS = os.environ.get("CRAWLER_FEED_URLS", "")
CRAWLER_INTERVAL_SECONDS = int(os.environ.get("CRAWLER_INTERVAL_SECONDS", "3600"))
GEMINI_API_KEY = os.environ.get("GEMINI_API_KEY", "")
CRAWLER_GEMINI_MODEL = os.environ.get("CRAWLER_GEMINI_MODEL", "gemini-2.5-flash")

if not GEMINI_API_KEY:
    logging.error("GEMINI_API_KEY is not set. Exiting.")
    sys.exit(1)

client = genai.Client(api_key=GEMINI_API_KEY)

def get_db_connection():
    """Establishes connection to SQLite with WAL mode and busy timeout."""
    conn = sqlite3.connect(DB_PATH, timeout=5.0)
    conn.execute("PRAGMA journal_mode = WAL;")
    conn.execute("PRAGMA busy_timeout = 5000;")
    conn.execute("PRAGMA foreign_keys = ON;")
    return conn


def check_already_crawled(url, title):
    """Checks if the URL or title is already registered in the prompts table."""
    conn = get_db_connection()
    try:
        cursor = conn.cursor()
        cursor.execute(
            "SELECT 1 FROM prompts WHERE seed_text = ? OR seed_text = ?",
            (url, title)
        )
        row = cursor.fetchone()
        return row is not None
    except sqlite3.Error as e:
        logging.error(f"Database error during crawl check: {e}")
        return False
    finally:
        conn.close()


def save_prompt_to_db(url, enriched_text, status, metadata_dict, tokens_used):
    """Saves the prompt record in SQLite database transactionally."""
    conn = get_db_connection()
    try:
        cursor = conn.cursor()
        metadata_json = json.dumps(metadata_dict) if metadata_dict else None
        cursor.execute(
            """
            INSERT INTO prompts (seed_text, enriched_text, status, tokens_used, builder_used, metadata)
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (url, enriched_text, status, tokens_used, f"crawler-{CRAWLER_GEMINI_MODEL}", metadata_json)
        )
        conn.commit()
        logging.info(f"Successfully saved prompt to database. Status: {status}, URL: {url}")
    except sqlite3.Error as e:
        logging.error(f"Database error saving prompt: {e}")
    finally:
        conn.close()


import urllib3

# Disable insecure request warnings for SSL fallback path
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)


def safe_requests_get(url, **kwargs):
    """Performs requests.get, falling back to verify=False if an SSL error occurs."""
    try:
        return requests.get(url, **kwargs)
    except requests.exceptions.SSLError as e:
        logging.warning(f"SSL certificate verification failed for {url}. Retrying with verify=False. Error: {e}")
        kwargs["verify"] = False
        return requests.get(url, **kwargs)
    except Exception as e:
        logging.error(f"Failed to fetch URL {url}: {e}")
        raise e


def parse_feed(feed_url):
    """Fetches and parses an RSS 2.0 or Atom feed."""
    logging.info(f"Parsing feed: {feed_url}")
    try:
        resp = safe_requests_get(feed_url, timeout=15)
        if resp.status_code != 200:
            logging.error(f"Failed to fetch feed {feed_url}, status code: {resp.status_code}")
            return []

        root = ET.fromstring(resp.content)
        items = []

        # Parse RSS 2.0 <item> entries
        for item in root.findall(".//item"):
            title_el = item.find("title")
            link_el = item.find("link")
            desc_el = item.find("description")

            title = title_el.text.strip() if title_el is not None and title_el.text else ""
            link = link_el.text.strip() if link_el is not None and link_el.text else ""
            desc = desc_el.text.strip() if desc_el is not None and desc_el.text else ""

            if link:
                items.append({"title": title, "link": link, "description": desc})

        # Parse Atom <entry> entries
        for entry in root.findall(".//{http://www.w3.org/2005/Atom}entry"):
            title_el = entry.find("{http://www.w3.org/2005/Atom}title")
            link_el = entry.find("{http://www.w3.org/2005/Atom}link")
            summary_el = (
                entry.find("{http://www.w3.org/2005/Atom}summary") or 
                entry.find("{http://www.w3.org/2005/Atom}content")
            )

            title = title_el.text.strip() if title_el is not None and title_el.text else ""
            link = link_el.attrib.get("href", "").strip() if link_el is not None else ""
            desc = summary_el.text.strip() if summary_el is not None and summary_el.text else ""

            if link:
                items.append({"title": title, "link": link, "description": desc})

        logging.info(f"Found {len(items)} items in feed {feed_url}")
        return items

    except Exception as e:
        logging.error(f"Failed to parse feed {feed_url}: {e}")
        return []


def scrape_article_text(url):
    """Downloads the article page HTML and extracts clean body text."""
    logging.info(f"Scraping article text from: {url}")
    try:
        resp = safe_requests_get(url, timeout=15, headers={
            "User-Agent": "BapBot/1.0 (Automated Content Crawler)"
        })
        if resp.status_code != 200:
            logging.warning(f"Failed to scrape page {url}, status code: {resp.status_code}")
            return ""

        soup = BeautifulSoup(resp.content, "html.parser")

        # Decompose script, style, navigation elements
        for element in soup(["script", "style", "header", "footer", "nav", "aside", "form"]):
            element.decompose()

        text = soup.get_text(separator=" ")
        # Clean whitespaces
        lines = (line.strip() for line in text.splitlines())
        chunks = (phrase.strip() for line in lines for phrase in line.split("  "))
        clean_text = "\n".join(chunk for chunk in chunks if chunk)

        # Cap text length to prevent context limit errors
        return clean_text[:15000]

    except Exception as e:
        logging.error(f"Error scraping article at {url}: {e}")
        return ""


def analyze_and_enrich_content(url, title, content):
    """
    Calls Gemini model to check safety policies ( mature/violence/explicit content ),
    evaluates visual descriptive quality, and builds the final video prompt and script.
    """
    logging.info(f"Analyzing article: {title}")

    # Use title + snippet if full scraping returned nothing
    analysis_text = content if content else f"Title: {title}"

    system_instruction = (
        "You are a strict safety screening and video prompt generation agent.\n"
        "Your task is to analyze scraped web content and generate structured outputs for an automated video creator.\n\n"
        
        "SAFETY DIRECTIVE (CRITICAL):\n"
        "Screen the content for mature themes, adult/explicit references, horror, gore, warfare, self-harm, weapons, "
        "crimes, or any violence-related topics. If any of these are present, you MUST reject the article.\n\n"
        
        "QUALITY DIRECTIVE:\n"
        "Evaluate if the content is visually descriptive or has high storytelling potential. If it is abstract, "
        "contains pure programming code without context, is an advertisement list, or is visually boring, reject it.\n\n"
        
        "If rejected, respond with JSON:\n"
        "{\n"
        "  \"approved\": false,\n"
        "  \"reason\": \"Detailed explanation of why the article was rejected (safety or quality reason)\"\n"
        "}\n\n"
        
        "If approved, respond with JSON:\n"
        "{\n"
        "  \"approved\": true,\n"
        "  \"reason\": \"Brief explanation of approval\",\n"
        "  \"prompt\": \"A highly descriptive, cinematic scene prompt (16:9 aspect ratio) ready for an AI video generator (Luma/Runway)\",\n"
        "  \"voice_script\": \"An engaging voiceover narration script (under 150 characters) summarizing the story for a TTS voice\",\n"
        "  \"youtube_title\": \"An engaging, clickable SEO video title under 70 characters\",\n"
        "  \"youtube_description\": \"An SEO-optimized description explaining the story\",\n"
        "  \"youtube_tags\": [\"tag1\", \"tag2\", \"tag3\"]\n"
        "}"
    )

    prompt = (
        f"URL: {url}\n"
        f"Title: {title}\n"
        f"Content:\n{analysis_text}"
    )

    try:
        response = client.models.generate_content(
            model=CRAWLER_GEMINI_MODEL,
            contents=prompt,
            config=types.GenerateContentConfig(
                system_instruction=system_instruction,
                response_mime_type="application/json",
                temperature=0.2,
                safety_settings=[
                    types.SafetySetting(category=types.HarmCategory.HARM_CATEGORY_HARASSMENT, threshold=types.HarmBlockThreshold.BLOCK_LOW_AND_ABOVE),
                    types.SafetySetting(category=types.HarmCategory.HARM_CATEGORY_HATE_SPEECH, threshold=types.HarmBlockThreshold.BLOCK_LOW_AND_ABOVE),
                    types.SafetySetting(category=types.HarmCategory.HARM_CATEGORY_SEXUALLY_EXPLICIT, threshold=types.HarmBlockThreshold.BLOCK_LOW_AND_ABOVE),
                    types.SafetySetting(category=types.HarmCategory.HARM_CATEGORY_DANGEROUS_CONTENT, threshold=types.HarmBlockThreshold.BLOCK_LOW_AND_ABOVE)
                ]
            )
        )

        text_out = response.text.strip() if response.text else ""
        if not text_out:
            logging.warning("Gemini API returned empty text.")
            return {"approved": False, "reason": "Empty Gemini response"}

        result = json.loads(text_out)
        
        # Calculate tokens used safely
        tokens = 0
        try:
            if hasattr(response, "usage_metadata") and response.usage_metadata:
                tokens = response.usage_metadata.total_token_count
        except Exception:
            pass

        result["tokens_used"] = tokens
        return result

    except json.JSONDecodeError as je:
        logging.error(f"Failed to parse JSON from Gemini response: {je}. Raw output: {text_out}")
        return {"approved": False, "reason": f"JSON parse error: {je}"}
    except Exception as e:
        # Catch API blocks, safety blocks or connection issues
        logging.error(f"Gemini API execution error: {e}")
        return None  # Return None to signal transient/API error


def process_feed_item(item):
    """Processes a single item scraped from the feeds."""
    url = item["link"]
    title = item["title"]

    if check_already_crawled(url, title):
        logging.debug(f"Skipping already crawled article: {title}")
        return

    logging.info(f"Processing new article: {title} ({url})")
    
    # Clean and scrape body text
    content = scrape_article_text(url)
    
    # Run Gemini Safety & Enrichment Validation
    analysis = analyze_and_enrich_content(url, title, content)
    if analysis is None:
        logging.warning(f"Skipping article '{title}' due to transient Gemini API or network failure.")
        return

    tokens_used = analysis.get("tokens_used", 0)

    if analysis.get("approved") is True:
        # Build metadata fields compatible with bap schema
        metadata_dict = {
            "voice_script": analysis.get("voice_script", ""),
            "youtube_title": analysis.get("youtube_title", ""),
            "youtube_description": analysis.get("youtube_description", ""),
            "youtube_tags": analysis.get("youtube_tags", []),
            "use_image_anchor": "true"
        }
        
        # Save UNUSED prompt for Go pipeline execution
        save_prompt_to_db(
            url=url,
            enriched_text=analysis.get("prompt", ""),
            status="UNUSED",
            metadata_dict=metadata_dict,
            tokens_used=tokens_used
        )
    else:
        reason = analysis.get("reason", "Unknown rejection reason")
        logging.info(f"Article rejected: {title}. Reason: {reason}")
        
        # Save as REJECTED to cache url and prevent reprocessing
        save_prompt_to_db(
            url=url,
            enriched_text=reason,
            status="REJECTED",
            metadata_dict=None,
            tokens_used=tokens_used
        )
    
def main():
    logging.info("Starting BAP Web Crawler service...")
    
    if not CRAWLER_FEED_URLS:
        logging.warning("No CRAWLER_FEED_URLS configured. Sleeping indefinitely.")
        while True:
            time.sleep(3600)

    feed_list = [f.strip() for f in CRAWLER_FEED_URLS.split(",") if f.strip()]
    logging.info(f"Active crawl feeds: {feed_list}")

    while True:
        for feed in feed_list:
            items = parse_feed(feed)
            for item in items:
                process_feed_item(item)
                # Sleep briefly between items to be nice to website hosts and respect API limits
                time.sleep(2)
        
        logging.info(f"Crawl cycle completed. Sleeping for {CRAWLER_INTERVAL_SECONDS} seconds...")
        time.sleep(CRAWLER_INTERVAL_SECONDS)

    # Close the client after saving the data to db
    client.close()

if __name__ == "__main__":
    main()
