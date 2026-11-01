package dockerx

import "fmt"

// maxLogChunk is the absolute demuxed-output cap; a frame crossing it marks
// the chunk truncated (the tail is dropped, the header is kept honest).
const maxLogChunk = 64 << 10

// DemuxStream parses the Docker multiplexed stream framing:
//
//	[STREAM_TYPE, 0, 0, 0, SIZE1..SIZE4 (big-endian), payload...]
//
// STREAM_TYPE 0 = stdout, 1 = stderr, 2 = progress (skipped). When the
// container allocates a TTY the Engine returns the RAW stream instead — a
// body with no valid frame header is treated as raw stdout (honest
// fallback, never mis-parsed). Output is bounded by maxBytes; truncation is
// REPORTED, not hidden (evidence honesty, CI-17 lesson).
func DemuxStream(body []byte, maxBytes int) (LogChunk, error) {
	if maxBytes <= 0 || maxBytes > maxLogChunk {
		maxBytes = maxLogChunk
	}
	chunk := LogChunk{}
	if len(body) == 0 {
		return chunk, nil
	}
	if !validFrame(body) {
		// TTY (raw) stream: bounded copy to stdout.
		if len(body) > maxBytes {
			chunk.Stdout = string(body[:maxBytes])
			chunk.Truncated = true
			return chunk, fmt.Errorf("çıktı sınırı aşıldı (tty akışı)")
		}
		chunk.Stdout = string(body)
		return chunk, nil
	}
	for off := 0; off+8 <= len(body); {
		typ := body[off]
		size := int(body[off+4])<<24 | int(body[off+5])<<16 | int(body[off+6])<<8 | int(body[off+7])
		off += 8
		if size < 0 || off+size > len(body) {
			// Malformed tail frame: stop, report truncated.
			chunk.Truncated = true
			return chunk, fmt.Errorf("bozuk log çerçevesi")
		}
		payload := body[off : off+size]
		off += size
		if typ == 0 || typ == 1 {
			if len(chunk.Stdout)+len(chunk.Stderr)+len(payload) > maxBytes {
				chunk.Truncated = true
				return chunk, fmt.Errorf("çıktı sınırı aşıldı")
			}
			if typ == 0 {
				chunk.Stdout += string(payload)
			} else {
				chunk.Stderr += string(payload)
			}
		}
		// typ 2 (progress/system) and unknown types are skipped by design.
	}
	return chunk, nil
}

// validFrame checks whether the body plausibly starts with a mux header
// (stream type 0/1/2, three zero bytes, sane declared size).
func validFrame(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	if b[0] > 2 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
		return false
	}
	size := int(b[4])<<24 | int(b[5])<<16 | int(b[6])<<8 | int(b[7])
	return size >= 0 && size <= maxLogChunk && 8+size <= len(b)
}
