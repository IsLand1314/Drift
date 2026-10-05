package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

const (
	maxSSELineBytes  = 4 << 20
	maxSSEEventBytes = 4 << 20
)

// SSESanitizer removes complete malformed SSE frames before a provider
// protocol parser sees them. Transport errors are returned unchanged.
type SSESanitizer struct {
	scanner *bufio.Scanner
	out     []byte
	event   []byte
	data    []byte
	hasData bool
	done    bool
	dropped int
}

func NewSSESanitizer(r io.Reader) *SSESanitizer {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxSSELineBytes)
	return &SSESanitizer{scanner: scanner}
}

func (s *SSESanitizer) DroppedFrames() int { return s.dropped }

func (s *SSESanitizer) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.done {
			return 0, io.EOF
		}
		if err := s.fill(); err != nil {
			if err == io.EOF {
				s.done = true
				return 0, io.EOF
			}
			return 0, err
		}
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

func (s *SSESanitizer) fill() error {
	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		if len(line) == 0 {
			if s.accept() {
				s.out = append(s.out, s.event...)
				s.out = append(s.out, '\n')
			} else if s.hasData || len(s.event) > 0 {
				s.dropped++
			}
			s.reset()
			if len(s.out) > 0 {
				return nil
			}
			continue
		}
		s.event = append(s.event, line...)
		s.event = append(s.event, '\n')
		if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimPrefix(line, []byte("data:"))
			data = bytes.TrimPrefix(data, []byte(" "))
			s.data = append(s.data, data...)
			s.data = append(s.data, '\n')
			s.hasData = true
		}
		if len(s.event) > maxSSEEventBytes {
			s.reset()
			s.dropped++
		}
	}
	if err := s.scanner.Err(); err != nil {
		return err
	}
	if len(s.event) > 0 {
		// Some providers omit the final blank separator before closing the
		// response. Preserve a valid terminal frame; only discard malformed
		// residual data.
		if s.accept() {
			s.out = append(s.out, s.event...)
			s.out = append(s.out, '\n')
		} else {
			s.dropped++
		}
		s.reset()
		if len(s.out) > 0 {
			return nil
		}
	}
	return io.EOF
}

func (s *SSESanitizer) accept() bool {
	if !s.hasData {
		return false
	}
	data := bytes.TrimSpace(s.data)
	if len(data) == 0 {
		return false
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		return true
	}
	return json.Valid(data)
}

func (s *SSESanitizer) reset() {
	s.event = s.event[:0]
	s.data = s.data[:0]
	s.hasData = false
}
