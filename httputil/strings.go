package httputil

// Standard HTTP Header names.
const (
	HeaderContentType   = "Content-Type"
	HeaderAuthorization = "Authorization"
	HeaderCacheControl = "Cache-Control"
	HeaderVary         = "Vary"
	BearerPrefix        = "Bearer "
)

// Standard MIME / Content Types.
const (
	ContentTypeJSON        = "application/json"
	ContentTypeJSONUTF8    = "application/json; charset=utf-8"
	ContentTypeProtobuf    = "application/x-protobuf"
	ContentTypeJS          = "application/javascript; charset=utf-8"
	ContentTypeCSS         = "text/css; charset=utf-8"
	ContentTypeHTML        = "text/html; charset=utf-8"
	ContentTypePNG         = "image/png"
	ContentTypeOctetStream = "application/octet-stream"
	ContentTypePlainText   = "text/plain; charset=utf-8"
)

// Standard API response / error messages.
const (
	MsgUnauthorized        = "Unauthorized"
	MsgForbidden           = "Forbidden"
	MsgNotFound            = "Not found"
	MsgInternalServerError = "Internal server error"
	MsgBadRequest          = "Bad request"
	MsgMethodNotAllowed    = "Method not allowed"
	MsgInvalidJSON         = "Invalid JSON payload"
	MsgPayloadTooLarge     = "Request payload exceeds maximum allowed size"
)
