package brk

import (
	"fmt"
	"strings"
	"testing"
)

func TestLogfRoutesDiagnostics(t *testing.T) {
	original := Logf
	t.Cleanup(func() { Logf = original })

	captured := []string{}
	Logf = func(format string, values ...any) {
		captured = append(captured, fmt.Sprintf(format, values...))
	}

	logDuplicate(UdpMessage{Sequence: 7, Address: "127.0.0.1", Port: 9000})

	if len(captured) != 1 {
		t.Fatalf("captured log count mismatch: expected %d, received %d", 1, len(captured))
	}
	if !strings.Contains(captured[0], "sequence=7") || !strings.Contains(captured[0], "source=127.0.0.1:9000") {
		t.Fatalf("captured log content mismatch: expected sequence and source fields, received %q", captured[0])
	}
}
