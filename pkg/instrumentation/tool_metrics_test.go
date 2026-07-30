// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package instrumentation

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestToolHandlerUntyped_TreatsResultIsErrorAsError is a regression test for
// the metrics wrapper missing the primary obs-mcp error pattern, where tool
// failures are encoded via CallToolResult.SetError/IsError while the Go error
// return stays nil (see resultutil.ToMCPResult).
func TestToolHandlerUntyped_TreatsResultIsErrorAsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewToolMetrics(reg)

	handler := func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := &mcp.CallToolResult{}
		result.SetError(errors.New("invalid query: bad request"))
		return result, nil
	}

	wrapped := ToolHandlerUntyped("test_tool", metrics, handler)
	result, err := wrapped(context.Background(), &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected nil Go error (MCP pattern), got %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected result.IsError to be true")
	}

	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "error")); got != 1 {
		t.Errorf("mcp_tool_calls_total{status=error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "success")); got != 0 {
		t.Errorf("mcp_tool_calls_total{status=success} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.toolErrorsTotal.WithLabelValues("test_tool", "client_error")); got != 1 {
		t.Errorf("mcp_tool_errors_total{error_type=client_error} = %v, want 1", got)
	}
}

func TestToolHandlerUntyped_Success(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewToolMetrics(reg)

	handler := func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	}

	wrapped := ToolHandlerUntyped("test_tool", metrics, handler)
	if _, err := wrapped(context.Background(), &mcp.CallToolRequest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "success")); got != 1 {
		t.Errorf("mcp_tool_calls_total{status=success} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "error")); got != 0 {
		t.Errorf("mcp_tool_calls_total{status=error} = %v, want 0", got)
	}
}

func TestToolHandlerUntyped_GoError(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewToolMetrics(reg)

	handler := func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("boom")
	}

	wrapped := ToolHandlerUntyped("test_tool", metrics, handler)
	if _, err := wrapped(context.Background(), &mcp.CallToolRequest{}); err == nil {
		t.Fatal("expected error to be returned")
	}

	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "error")); got != 1 {
		t.Errorf("mcp_tool_calls_total{status=error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.toolErrorsTotal.WithLabelValues("test_tool", "internal_error")); got != 1 {
		t.Errorf("mcp_tool_errors_total{error_type=internal_error} = %v, want 1", got)
	}
}

func TestToolHandler_TreatsResultIsErrorAsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewToolMetrics(reg)

	type input struct{}
	type output struct{}

	handler := func(_ context.Context, _ *mcp.CallToolRequest, _ input) (*mcp.CallToolResult, output, error) {
		result := &mcp.CallToolResult{}
		result.SetError(errors.New("validation failed: missing field"))
		return result, output{}, nil
	}

	wrapped := ToolHandler[input, output]("test_tool", metrics, handler)
	result, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, input{})
	if err != nil {
		t.Fatalf("expected nil Go error (MCP pattern), got %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected result.IsError to be true")
	}

	if got := testutil.ToFloat64(metrics.toolCallsTotal.WithLabelValues("test_tool", "error")); got != 1 {
		t.Errorf("mcp_tool_calls_total{status=error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.toolErrorsTotal.WithLabelValues("test_tool", "client_error")); got != 1 {
		t.Errorf("mcp_tool_errors_total{error_type=client_error} = %v, want 1", got)
	}
}

func TestEffectiveToolError(t *testing.T) {
	tests := []struct {
		name      string
		result    *mcp.CallToolResult
		err       error
		wantError bool
	}{
		{
			name:      "nil result and nil error",
			wantError: false,
		},
		{
			name:      "go error takes precedence",
			err:       errors.New("boom"),
			wantError: true,
		},
		{
			name:      "successful result",
			result:    &mcp.CallToolResult{},
			wantError: false,
		},
		{
			name: "result with IsError set via SetError",
			result: func() *mcp.CallToolResult {
				r := &mcp.CallToolResult{}
				r.SetError(errors.New("bad request"))
				return r
			}(),
			wantError: true,
		},
		{
			name: "result with IsError true but no captured error falls back to text content",
			result: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "something went wrong"}},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveToolError(tt.result, tt.err)
			if (got != nil) != tt.wantError {
				t.Errorf("effectiveToolError() = %v, wantError %v", got, tt.wantError)
			}
		})
	}
}
