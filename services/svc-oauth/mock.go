package main

// mockProvider 开发/E2E 用：直接跳回 callback，externalId 固定。
type mockProvider struct{}

func newMockProvider() *mockProvider { return &mockProvider{} }

func (p *mockProvider) ID() string { return "mock" }

func (p *mockProvider) Start(state string) (string, error) {
	return selfURL() + "/api/oauth/mock/callback?code=mock-code&state=" + state, nil
}

func (p *mockProvider) Callback(_ string) (*ExternalProfile, error) {
	return &ExternalProfile{
		Provider:    "mock",
		Subject:     "mock-user-1",
		DisplayName: "Mock User",
	}, nil
}
