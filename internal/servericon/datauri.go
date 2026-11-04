package servericon

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// MaxEncodedLen caps the whole data: URI written to status.icon at 65536
// characters; the CRD enforces the same MaxLength.
const MaxEncodedLen = 64 << 10

const mimeSVG = "image/svg+xml"

var allowedMIMEs = map[string]bool{
	mimeSVG:      true,
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// errInvalid marks an icon that will not become usable by asking again: a
// disallowed type, a size over the cap, a scheme muster does not fetch.
var errInvalid = errors.New("invalid icon")

func allowedMIME(mime string) bool { return allowedMIMEs[mime] }

// normalizeMIME drops parameters and case: "Image/PNG; q=1" is "image/png".
func normalizeMIME(mime string) string {
	mime, _, _ = strings.Cut(mime, ";")
	return strings.ToLower(strings.TrimSpace(mime))
}

// encode validates an image against its declared MIME type and returns it
// as a data: URI.
func encode(declared string, data []byte) (string, error) {
	mime := normalizeMIME(declared)
	if !allowedMIME(mime) {
		return "", fmt.Errorf("%w: type %q not allowed", errInvalid, declared)
	}
	if sniffed := sniff(data); sniffed != mime {
		return "", fmt.Errorf("%w: declared %s, content is %q", errInvalid, mime, sniffed)
	}
	uri := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	if len(uri) > MaxEncodedLen {
		return "", fmt.Errorf("%w: %d bytes encoded, over %d", errInvalid, len(uri), MaxEncodedLen)
	}
	return uri, nil
}

// sniff returns the allowed MIME type the content is, or "" for anything else.
func sniff(data []byte) string {
	switch detected := http.DetectContentType(data); detected {
	case "image/png", "image/jpeg", "image/webp":
		return detected
	}
	if looksLikeSVG(data) {
		return mimeSVG
	}
	return ""
}

func looksLikeSVG(data []byte) bool {
	head := data[:min(len(data), 4096)]
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	head = bytes.TrimSpace(head)
	if !bytes.HasPrefix(head, []byte("<")) {
		return false
	}
	return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}

// FromDataURI validates a data: URI icon and returns it in the canonical
// base64 form.
func FromDataURI(src string) (string, error) {
	rest, ok := strings.CutPrefix(src, "data:")
	if !ok {
		return "", fmt.Errorf("%w: not a data: URI", errInvalid)
	}
	if len(rest) > MaxEncodedLen*2 {
		return "", fmt.Errorf("%w: data: URI over %d bytes", errInvalid, MaxEncodedLen*2)
	}
	header, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return "", fmt.Errorf("%w: data: URI without payload", errInvalid)
	}
	params := strings.Split(header, ";")
	isBase64 := false
	for _, p := range params[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			isBase64 = true
		}
	}
	var data []byte
	var err error
	if isBase64 {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
		}
	} else {
		var s string
		s, err = url.PathUnescape(payload)
		data = []byte(s)
	}
	if err != nil {
		return "", fmt.Errorf("%w: data: URI payload: %v", errInvalid, err)
	}
	return encode(params[0], data)
}
