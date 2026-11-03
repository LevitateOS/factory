package mcpwire

import (
	"github.com/levitateos/factory/pkg/factory"
)

func IsNativeTask(tool factory.MCPTool) bool {
	return tool == factory.MCPToolStartTask || tool == factory.MCPToolReleaseOutput
}
