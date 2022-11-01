package consul

import (
	"encoding/json"
	"fmt"
	"github.com/hashicorp/consul/api"
)

type KV struct {
	*api.KV
}

type SSConsulConfig struct {
	ID      string
	Address string
	Port    int
}

func NewKV(c *Client) *KV {
	return &KV{
		c.KV(),
	}
}

func (kv *KV) PutSelfToArray(key string, cnf SSConsulConfig) error {

	val, _, err := kv.Get(key, nil)

	var ssConfigs []SSConsulConfig

	err = json.Unmarshal(val.Value, &ssConfigs)
	if err != nil {
		return fmt.Errorf("error unmarshal when get arr from consul: %w", err)
	}

	ssConfigs = append(ssConfigs, cnf)

	v, err := json.Marshal(ssConfigs)
	if err != nil {
		return fmt.Errorf("error to marshal when set value: %w", err)
	}

	p := &api.KVPair{Key: key, Value: v}

	_, err = kv.Put(p, nil)
	if err != nil {
		return fmt.Errorf("error instergin KV: %w", err)
	}

	return nil
}
