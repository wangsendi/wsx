package wsx

import "encoding/json"

type Envelope struct {
	Ver  int             `json:"ver"`
	Type string          `json:"type"`
	TS   int64           `json:"ts"`
	Data json.RawMessage `json:"data"`
}

func (e Envelope) Clone() Envelope {
	return Envelope{
		Ver:  e.Ver,
		Type: e.Type,
		TS:   e.TS,
		Data: append(json.RawMessage(nil), e.Data...),
	}
}
