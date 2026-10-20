package health

import (
	"encoding/json"
	"net/http"
)

// HTTPHandler exposes GET /live and GET /ready over a registry.
func HTTPHandler(registry *Registry) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		var report Report
		switch request.URL.Path {
		case "/live":
			if registry == nil {
				report = unavailableReport()
			} else {
				report = registry.CheckLiveness(request.Context())
			}
		case "/ready":
			if registry == nil {
				report = unavailableReport()
			} else {
				report = registry.CheckReadiness(request.Context())
			}
		default:
			http.NotFound(writer, request)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		status := http.StatusOK
		if !report.Healthy {
			status = http.StatusServiceUnavailable
		}
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(report)
	})
}
