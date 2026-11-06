package execution

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/strictjson"
)

// NativeIntent is the nonsecret effect binding produced from one already
// admitted launch. It is not current product authority and carries no prompt,
// MCP secret or provider credential. Retain its encoded bytes before effects;
// decoding/recovery must not regenerate them from today's selections.
type NativeIntent struct {
	Kind         string       `json:"kind"`
	Version      int          `json:"version"`
	Run          string       `json:"run"`
	Workspace    string       `json:"workspace"`
	Role         string       `json:"role"`
	SourceCommit string       `json:"source_commit"`
	Connection   string       `json:"connection"`
	Deadline     time.Time    `json:"deadline"`
	Native       NativeLaunch `json:"native"`
}

var ErrNativeIntent = errors.New("native launch intent differs from its captured binding")

func (i NativeIntent) Validate() error {
	if i.Kind != "factory_native_launch_intent" || i.Version != 1 ||
		!project.ValidFactoryRunID(i.Run) || !project.ValidID(i.Workspace) ||
		!project.ValidFactoryRole(i.Role) || !project.ValidCommit(i.SourceCommit) ||
		!collaboration.ValidText(i.Connection, 128) || i.Deadline.IsZero() ||
		i.Native.Validate() != nil || i.Native.TrustMode != "isolated_system_worker" {
		return ErrNativeIntent
	}
	return nil
}

func ParseNativeIntent(raw string) (NativeIntent, error) {
	var intent NativeIntent
	if len(raw) == 0 || len(raw) > 16384 || strictjson.Decode(strings.NewReader(raw), &intent) != nil || intent.Validate() != nil {
		return NativeIntent{}, ErrNativeIntent
	}
	return intent, nil
}

// EncodeNativeIntent copies the defining semantic launch inputs. Call only
// after current Go admission; shape validation alone does not grant authority.
func EncodeNativeIntent(request LaunchRequest) (string, error) {
	if request.Validate() != nil || request.Kind != collaboration.Native || request.Native == nil {
		return "", ErrNativeIntent
	}
	run := request.Input.Run
	intent := NativeIntent{Kind: "factory_native_launch_intent", Version: 1,
		Run: run.ID, Workspace: run.Project, Role: run.Role, SourceCommit: run.SourceCommit,
		Connection: run.Connection, Deadline: run.Deadline, Native: *request.Native}
	return intent.Encode()
}

// Encode preserves the single defining intent shape for transactional capture
// and for the semantic launch consumer. Shape validity is not admission.
func (intent NativeIntent) Encode() (string, error) {
	if intent.Validate() != nil {
		return "", ErrNativeIntent
	}
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > 16384 {
		return "", ErrNativeIntent
	}
	return string(raw), nil
}
