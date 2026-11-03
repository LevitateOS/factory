package mcpwire

import (
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// CurrentView is the existing bounded worker context wire, never permission to
// execute work. Source reads and current authority belong to application/host.
type CurrentView struct {
	Current factory.CurrentContext
	Page    ContextPage
	Hold    bool
}

// RenderCurrent keeps current-role metadata small and its complete typed fields
// in the finite UTF-8-safe stream. The caller supplies an admitted capability;
// omitNativeTasks reflects a bound server with no actual task-command port.
func RenderCurrent(current factory.CurrentContext, cap factory.MCPCapability, payload map[string]any, omitNativeTasks bool) (CurrentView, error) {
	current.Role.Tools = factory.MCPDiscovery(cap.Profile)
	if omitNativeTasks {
		filtered := current.Role.Tools[:0]
		for _, tool := range current.Role.Tools {
			if !IsNativeTask(tool) {
				filtered = append(filtered, tool)
			}
		}
		current.Role.Tools = filtered
	}
	var err error
	for _, tool := range current.Role.Tools {
		if !factory.MCPIsTerminal(tool) || tool == factory.MCPToolFinishIncomplete {
			continue
		}
		if shape := ResultSchema(tool); shape != nil {
			current.Role.ResultTool = tool
			current.Role.ResultSchema, err = json.Marshal(shape)
			if err != nil {
				return CurrentView{}, err
			}
		}
	}
	page, err := pageCurrentRole(current.Role, payload)
	if errors.Is(err, errCurrentContextBound) {
		current.Role = factory.CurrentRoleContext{Kind: cap.Profile}
		current.Evidence.Availability = "incomplete"
		current.Evidence.MissingReasons = append(current.Evidence.MissingReasons, "required_current_collection_bound")
		page.Availability, page.MissingReason = "unavailable", "required_current_collection_bound"
	} else if err != nil {
		return CurrentView{}, err
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return CurrentView{}, err
	}
	if len(encoded) > 8192 {
		current.Role = factory.CurrentRoleContext{Kind: cap.Profile, RecordSource: "required_current"}
	}
	return CurrentView{Current: current, Page: page, Hold: current.Evidence.Availability != "complete"}, nil
}
