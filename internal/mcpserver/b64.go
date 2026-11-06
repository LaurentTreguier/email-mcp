package mcpserver

import "encoding/base64"

func b64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func boolPtr(b bool) *bool { return &b }
