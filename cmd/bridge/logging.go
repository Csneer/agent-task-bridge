package main

import (
	"errors"
	"io"
	"log/slog"

	"github.com/Csneer/agent-task-bridge/bridge"
)

func runtimeLogger(format string, output io.Writer) (*slog.Logger, error) {
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(output, nil)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(output, nil)), nil
	default:
		return nil, errors.New("log-format must be text or json")
	}
}

func loggingOptions(logger *slog.Logger, key []byte) bridge.Options {
	return bridge.Options{
		Key: key,
		ObserveHTTP: func(o bridge.HTTPObservation) {
			// The application supplies bounded labels, never request data.
			logger.Info("request completed", "method", o.Method, "status", o.Status,
				"error_kind", o.ErrorKind, "duration_ms", float64(o.Duration.Microseconds())/1000)
		},
		ObserveMaintenance: func(o bridge.MaintenanceObservation) {
			// Successful one-second sweeps stay in counters, not the log stream.
			if o.ErrorKind != "" {
				logger.Error("maintenance failed", "error_kind", o.ErrorKind,
					"duration_ms", float64(o.Duration.Microseconds())/1000)
			}
		},
	}
}
