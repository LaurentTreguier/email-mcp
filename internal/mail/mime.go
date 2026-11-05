package mail

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/textproto"
)

// parseHeaders parses a raw RFC 5322 header block into a map (first value per key).
func parseHeaders(raw []byte) map[string]string {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for fields := h.Fields(); fields.Next(); {
		key := fields.Key()
		v := fields.Value()
		if _, ok := out[key]; !ok {
			out[key] = v
		}
	}
	return out
}

// extractBodies walks a raw message and returns the text/plain and text/html bodies.
func extractBodies(raw []byte) (text, html string) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return string(raw), ""
	}
	var walk func(e *message.Entity, depth int)
	walk = func(e *message.Entity, depth int) {
		mediaType, _, err := e.Header.ContentType()
		if err != nil {
			mediaType = "text/plain"
		}
		if strings.HasPrefix(mediaType, "multipart/") {
			mr := e.MultipartReader()
			for {
				p, err := mr.NextPart()
				if err != nil {
					return
				}
				walk(p, depth+1)
			}
		}
		body, err := io.ReadAll(e.Body)
		if err != nil {
			return
		}
		switch mediaType {
		case "text/plain":
			if text == "" {
				text = string(body)
			}
		case "text/html":
			if html == "" {
				html = string(body)
			}
		}
	}
	walk(entity, 0)
	return text, html
}

// walkBodyStructure converts a BODYSTRUCTURE tree into a flat attachment list.
func walkBodyStructure(bs imap.BodyStructure) []AttachmentInfo {
	var out []AttachmentInfo
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		id := ""
		for i, n := range path {
			if i > 0 {
				id += "."
			}
			id += fmt.Sprint(n)
		}
		info := AttachmentInfo{
			PartID:    id,
			MediaType: part.MediaType(),
		}
		if disp := part.Disposition(); disp != nil {
			info.Disposition = disp.Value
			if fn, ok := disp.Params["filename"]; ok {
				info.Filename = fn
			}
		}
		if sp, ok := part.(*imap.BodyStructureSinglePart); ok {
			info.Size = int64(sp.Size)
			if info.Filename == "" {
				info.Filename = sp.Filename()
			}
		}
		// Skip the root container; keep everything else that is an attachment
		// or a non-text part.
		if len(path) > 0 && (info.Disposition == "attachment" || !strings.HasPrefix(info.MediaType, "text/")) {
			out = append(out, info)
		}
		return true
	})
	return out
}
