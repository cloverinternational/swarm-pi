package chat

import "testing"

func TestTaskManageRenderConfigHidesOperationParameters(t *testing.T) {
	settings := NewDefaultRenderSettings()
	config := settings.ToolConfigs["TaskManage"]
	if config == nil {
		t.Fatal("TaskManage render config is missing")
	}
	if config.ShowParams {
		t.Fatal("TaskManage should render resulting task state, not raw operation parameters")
	}
}
