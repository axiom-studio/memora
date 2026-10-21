package federation

import (
	"crypto/md5"
	"fmt"
)

// nid maps a readable test name to a stable UUIDv4, standing in for a
// node ID.
func nid(name string) string {
	h := md5.Sum([]byte(name))
	h[6] = h[6]&0x0f | 0x40
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
