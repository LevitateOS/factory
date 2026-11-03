package control

import (
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

func inspectGraphPacket(codec factory.GraphCodec, raw json.RawMessage) (factory.GraphPacketFacts, error) {
	if codec == nil {
		return factory.GraphPacketFacts{}, errors.New("graph codec is unavailable")
	}
	return codec.InspectGraph(raw)
}

func canonicalGraphPacket(codec factory.GraphCodec, raw json.RawMessage) (json.RawMessage, error) {
	facts, err := inspectGraphPacket(codec, raw)
	return facts.Canonical, err
}
