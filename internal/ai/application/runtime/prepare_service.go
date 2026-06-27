package runtime

import "agent-desk/internal/ai/runtime/registry"

func newPrepareService(catalog *toolCatalog) *prepareService {
	return &prepareService{catalog: catalog}
}

type prepareService struct {
	catalog *toolCatalog
}

func (s *prepareService) prepareToolsForRun(req Request) (*registry.ToolSet, error) {
	if req.ToolSet != nil {
		return req.ToolSet, nil
	}
	return s.catalog.resolveForRun(req)
}

func (s *prepareService) prepareToolsForResume(req ResumeRequest) (*registry.ToolSet, error) {
	if req.ToolSet != nil {
		return req.ToolSet, nil
	}
	return s.catalog.resolveForResume(req)
}
