package mcpwire

import (
	"github.com/levitateos/sodaos/internal/factory"
)

func IsSystemOne(tool factory.MCPTool) bool {
	return tool == factory.MCPToolSystemOneNoul || tool == factory.MCPToolSystemOneChoice || tool == factory.MCPToolSystemOneScore
}
