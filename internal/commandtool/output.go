package commandtool

import "strings"

const truncationMarker = "\n... output truncated ...\n"

type headTailBuffer struct {
	limit int
	head  []byte
	tail  []byte
	total int64
}

func newHeadTailBuffer(limit int) *headTailBuffer {
	if limit < 2 {
		limit = 2
	}
	return &headTailBuffer{limit: limit}
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.total += int64(written)

	headLimit := b.limit / 2
	if len(b.head) < headLimit {
		count := min(headLimit-len(b.head), len(p))
		b.head = append(b.head, p[:count]...)
		p = p[count:]
	}

	tailLimit := b.limit - headLimit
	if len(p) >= tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-tailLimit:]...)
		return written, nil
	}
	if overflow := len(b.tail) + len(p) - tailLimit; overflow > 0 {
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:len(b.tail)-overflow]
	}
	b.tail = append(b.tail, p...)
	return written, nil
}

func (b *headTailBuffer) Truncated() bool {
	return b.total > int64(len(b.head)+len(b.tail))
}

func (b *headTailBuffer) String() string {
	var result strings.Builder
	result.Grow(len(b.head) + len(b.tail) + len(truncationMarker))
	result.Write(b.head)
	if b.Truncated() {
		result.WriteString(truncationMarker)
	}
	result.Write(b.tail)
	return result.String()
}
