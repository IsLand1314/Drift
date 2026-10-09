package observability

import (
	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/audit"
)

// AuditWriter preserves Drift's audit-first event path while mirroring the
// same sanitized dimensions to the invocation recorder. It does not change
// audit errors or close semantics.
type AuditWriter struct {
	next     audit.Writer
	recorder *Recorder
}

func WrapAuditWriter(next audit.Writer, recorder *Recorder) *AuditWriter {
	return &AuditWriter{next: next, recorder: recorder}
}

func (w *AuditWriter) Append(event agent.Event) error {
	if w.recorder != nil {
		w.recorder.Observe(event)
	}
	return w.next.Append(event)
}

func (w *AuditWriter) Close() error { return w.next.Close() }
