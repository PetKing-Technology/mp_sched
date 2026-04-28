package k8s

import (
	"context"
	"fmt"

	"mp_sched/internal/model"
	"mp_sched/internal/provider"
)

// Client 为 Kubernetes Provider 的占位实现，后续可接入 client-go。
type Client struct{}

func New() *Client { return &Client{} }

func (c *Client) Name() string { return "k8s" }

func (c *Client) ResourceCheck(ctx context.Context, t *model.Task) (*provider.ResourceCheckResult, error) {
	_ = ctx
	_ = t
	return &provider.ResourceCheckResult{OK: true, Reason: "stub"}, nil
}

func (c *Client) Run(ctx context.Context, t *model.Task) (string, error) {
	_ = ctx
	return fmt.Sprintf("k8s-stub-%s", t.TaskID), nil
}

func (c *Client) Status(ctx context.Context, t *model.Task) (*provider.RuntimeStatus, error) {
	_ = ctx
	_ = t
	return &provider.RuntimeStatus{Phase: "unknown", Message: "k8s stub"}, nil
}

func (c *Client) Stop(ctx context.Context, t *model.Task) error {
	_ = ctx
	_ = t
	return nil
}

var _ provider.Provider = (*Client)(nil)
