// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package instrumentation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ToolMetrics holds Prometheus metrics for MCP tool invocations.
type ToolMetrics struct {
	toolCallsTotal   *prometheus.CounterVec
	toolCallDuration *prometheus.HistogramVec
	toolErrorsTotal  *prometheus.CounterVec
}

// NewToolMetrics creates and registers new tool metrics with the provided registry.
func NewToolMetrics(reg prometheus.Registerer) *ToolMetrics {
	if reg == nil {
		return nil
	}

	durationBuckets := []float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120, 240, 360, 720}
	bucketFactor := 1.1
	maxBuckets := uint32(100)

	return &ToolMetrics{
		toolCallsTotal: promauto.With(reg).NewCounterVec(
			prometheus.CounterOpts{
				Name: "mcp_tool_calls_total",
				Help: "Total number of MCP tool calls by tool name and status.",
			},
			[]string{"tool_name", "status"},
		),

		toolCallDuration: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Name:                           "mcp_tool_call_duration_seconds",
				Help:                           "Duration of MCP tool calls by tool name and status.",
				Buckets:                        durationBuckets,
				NativeHistogramBucketFactor:    bucketFactor,
				NativeHistogramMaxBucketNumber: maxBuckets,
			},
			[]string{"tool_name", "status"},
		),

		toolErrorsTotal: promauto.With(reg).NewCounterVec(
			prometheus.CounterOpts{
				Name: "mcp_tool_errors_total",
				Help: "Total number of MCP tool errors by tool name and error type.",
			},
			[]string{"tool_name", "error_type"},
		),
	}
}

// ToolHandler wraps an MCP tool handler with metrics instrumentation.
// It records call counts, durations, and error details for each tool invocation.
func ToolHandler[I, O any](
	toolName string,
	metrics *ToolMetrics,
	handler mcp.ToolHandlerFor[I, O],
) mcp.ToolHandlerFor[I, O] {
	// If metrics is nil, return the handler unchanged (for tests or when metrics disabled)
	if metrics == nil {
		return handler
	}

	return func(ctx context.Context, req *mcp.CallToolRequest, input I) (*mcp.CallToolResult, O, error) {
		start := time.Now()

		// Execute the actual handler
		result, output, err := handler(ctx, req, input)

		// Record metrics
		duration := time.Since(start).Seconds()
		status := "success"
		if resultErr := effectiveToolError(result, err); resultErr != nil {
			status = "error"
			errorType := categorizeError(resultErr)
			metrics.toolErrorsTotal.WithLabelValues(toolName, errorType).Inc()
		}

		metrics.toolCallsTotal.WithLabelValues(toolName, status).Inc()
		metrics.toolCallDuration.WithLabelValues(toolName, status).Observe(duration)

		return result, output, err
	}
}

// ToolHandlerUntyped wraps an untyped MCP tool handler with metrics instrumentation.
// This is the equivalent of InstrumentToolHandler for handlers that use the base mcp.ToolHandler type
// (i.e., those registered via the api.Toolset interface) rather than the generic mcp.ToolHandlerFor[I, O].
func ToolHandlerUntyped(
	toolName string,
	metrics *ToolMetrics,
	handler mcp.ToolHandler,
) mcp.ToolHandler {
	if metrics == nil {
		return handler
	}

	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		result, err := handler(ctx, req)

		duration := time.Since(start).Seconds()
		status := "success"
		if resultErr := effectiveToolError(result, err); resultErr != nil {
			status = "error"
			errorType := categorizeError(resultErr)
			metrics.toolErrorsTotal.WithLabelValues(toolName, errorType).Inc()
		}

		metrics.toolCallsTotal.WithLabelValues(toolName, status).Inc()
		metrics.toolCallDuration.WithLabelValues(toolName, status).Observe(duration)

		return result, err
	}
}

// effectiveToolError returns the error that should be recorded for metrics
// purposes. The MCP pattern used throughout obs-mcp (see resultutil.ToMCPResult)
// encodes tool failures in the CallToolResult via IsError/SetError while
// returning a nil Go error, so a plain `err != nil` check misses most tool
// errors. This treats a result with IsError=true as an error too, preferring
// the underlying error captured by SetError and falling back to the result's
// text content when it is unavailable.
func effectiveToolError(result *mcp.CallToolResult, err error) error {
	if err != nil {
		return err
	}
	if result == nil || !result.IsError {
		return nil
	}
	if resultErr := result.GetError(); resultErr != nil {
		return resultErr
	}
	return errors.New(resultErrorText(result))
}

// resultErrorText extracts a human-readable error message from a
// CallToolResult's text content, used when no underlying error was captured
// via SetError.
func resultErrorText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	return "tool call returned an error result"
}

// categorizeError categorizes errors into types for metrics labeling.
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}

	errMsg := strings.ToLower(err.Error())

	if strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "validation") ||
		strings.Contains(errMsg, "bad request") {
		return "client_error"
	}

	if strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "deadline exceeded") {
		return "timeout"
	}

	return "internal_error"
}
