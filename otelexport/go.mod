module github.com/beautiful-majestic-dolphin/gorapide/otelexport

go 1.22.0

require (
	github.com/beautiful-majestic-dolphin/gorapide v0.0.0
	go.opentelemetry.io/otel/trace v1.35.0
)

require go.opentelemetry.io/otel v1.35.0 // indirect

replace github.com/beautiful-majestic-dolphin/gorapide => ../
