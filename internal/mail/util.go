package mail

import (
	"encoding/base64"
	"time"
)

var nowFunc = time.Now

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
