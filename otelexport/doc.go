// Package otelexport provides live OpenTelemetry trace export for gorapide
// architectures. It streams poset events as OTLP spans to a collector
// during execution, using the existing WithObserver hook.
package otelexport
