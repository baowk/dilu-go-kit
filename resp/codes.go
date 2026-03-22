package resp

// Standard error codes. Services can define additional codes
// with service-specific prefixes (e.g. 01xxxx for mf-user).
const (
	CodeOK = 200

	// 400xx — Parameter errors
	CodeInvalidParam = 40001
	CodeMissingField = 40002
	CodeFormatError  = 40003

	// 401xx — Authentication errors
	CodeUnauthorized  = 40101
	CodeTokenExpired  = 40102
	CodeTokenInvalid  = 40103

	// 403xx — Permission errors
	CodeForbidden     = 40301
	CodeNoPermission  = 40302

	// 404xx — Not found
	CodeNotFound      = 40401

	// 409xx — Conflict
	CodeConflict      = 40901

	// 429xx — Rate limit
	CodeTooManyReqs   = 42901

	// 500xx — Server errors
	CodeInternal      = 50000
	CodeDBError       = 50001
	CodeServiceDown   = 50002
)
