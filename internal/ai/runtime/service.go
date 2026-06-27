package runtime

import (
	"context"

	applicationruntime "agent-desk/internal/ai/application/runtime"
)

var Service = newService()

func newService() *service {
	return &service{
		app: applicationruntime.NewService(),
	}
}

type service struct {
	app *applicationruntime.Service
}

func (s *service) Run(ctx context.Context, req applicationruntime.Request) (*applicationruntime.Summary, error) {
	return s.app.Run(ctx, req)
}

func (s *service) Resume(ctx context.Context, req applicationruntime.ResumeRequest) (*applicationruntime.Summary, error) {
	return s.app.Resume(ctx, req)
}
