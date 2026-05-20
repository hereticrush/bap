/*
 * internal/adapter/video/uploader.go
 *
 * AssetUploader uploads local media for use in provider API requests.
 */
package video

import "context"

/*
 * AssetUploader uploads a local file and returns a provider-specific URI
 * (e.g. runway://) suitable for promptImage and similar fields.
 */
type AssetUploader interface {
	UploadImage(ctx context.Context, localPath string) (providerURI string, err error)
}
