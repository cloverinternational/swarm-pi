package chrome

import "fmt"

// DeliveryClass controls whether a request may be replayed after dispatch uncertainty.
type DeliveryClass string

const (
	DeliveryReadSafe       DeliveryClass = "read_safe"
	DeliveryStateChange    DeliveryClass = "state_change"
	DeliveryExternalEffect DeliveryClass = "external_effect"
)

// ClassifyDelivery returns the normative class for a tool invocation.
func ClassifyDelivery(tool string, input map[string]any) (DeliveryClass, error) {
	switch tool {
	case "chrome_tabs", "chrome_read_page", "chrome_get_page_text", "chrome_find":
		return DeliveryReadSafe, nil
	case "chrome_new_tab", "chrome_navigate", "chrome_resize_window", "chrome_close_session":
		return DeliveryStateChange, nil
	case "chrome_form_input", "chrome_javascript", "chrome_upload":
		return DeliveryExternalEffect, nil
	case "chrome_session_status":
		if ensure, _ := input["ensure_open"].(bool); ensure {
			return DeliveryStateChange, nil
		}
		return DeliveryReadSafe, nil
	case "chrome_console", "chrome_network":
		if clear, _ := input["clear"].(bool); clear {
			return DeliveryStateChange, nil
		}
		return DeliveryReadSafe, nil
	case "chrome_computer":
		action, _ := input["action"].(string)
		switch action {
		case "screenshot", "zoom", "wait":
			return DeliveryReadSafe, nil
		case "hover", "scroll", "scroll_to":
			return DeliveryStateChange, nil
		case "left_click", "right_click", "double_click", "triple_click", "type", "key", "drag":
			return DeliveryExternalEffect, nil
		default:
			return "", fmt.Errorf("chrome: unknown computer action %q", action)
		}
	default:
		return "", fmt.Errorf("chrome: unknown tool %q", tool)
	}
}
