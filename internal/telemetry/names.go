package telemetry

// InitLogger 与本包约定：与 cmd/mp-controller、cmd/mp-worker 传入的 service 名一致。
const (
	ServiceController = "mp-controller"
	ServiceWorker     = "mp-worker"
)
