package wsx

import (
	"encoding/json"
	"fmt"
)

type Codec interface {
	Encode(env Envelope) ([]byte, error)
	Decode(frame []byte) (Envelope, error)
}

type JSONCodec struct{}

func (JSONCodec) Encode(env Envelope) ([]byte, error) {
	return json.Marshal(env)
}

func (JSONCodec) Decode(frame []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(frame, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	return env, nil
}
