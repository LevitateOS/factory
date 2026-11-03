package mcpwire

import (
	"github.com/levitateos/sodaos/internal/factory"
)

func IsNativeTask(tool factory.MCPTool) bool {
	return tool == factory.MCPToolStartTask || tool == factory.MCPToolReleaseOutput
}
