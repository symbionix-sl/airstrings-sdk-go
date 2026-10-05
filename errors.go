package airstrings

import (
	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/fetch"
)

// Errors delivered to Config.OnError and returned by Refresh wrap one of these;
// test with errors.Is.
var (
	// ErrUnknownKeyID: the bundle's key_id is not one of Config.PublicKeys.
	ErrUnknownKeyID = bundle.ErrUnknownKeyID
	// ErrSignature: the Ed25519 signature does not match the bundle content.
	ErrSignature = bundle.ErrSignature
	// ErrUnsupportedFormatVersion: the bundle uses a format_version this SDK does not know.
	ErrUnsupportedFormatVersion = bundle.ErrUnsupportedFormatVersion
	// ErrInvalidSignatureEncoding: the signature is not 64 bytes of unpadded base64url.
	ErrInvalidSignatureEncoding = bundle.ErrInvalidSignatureEncoding
	// ErrInvalidKeyIDEncoding: a configured public key is not standard base64 of 32 bytes.
	ErrInvalidKeyIDEncoding = bundle.ErrInvalidKeyIDEncoding
	// ErrBundleDecode: the bundle is not a well-formed bundle envelope.
	ErrBundleDecode = bundle.ErrBundleDecode
	// ErrProjectMismatch: a verified bundle belongs to another project.
	ErrProjectMismatch = bundle.ErrProjectMismatch
	// ErrLocaleMismatch: a verified bundle is for another locale than requested.
	ErrLocaleMismatch = bundle.ErrLocaleMismatch
	// ErrHTTPStatus: the CDN answered with an unexpected HTTP status.
	ErrHTTPStatus = fetch.ErrHTTPStatus
)
