package consul

import (
	"fmt"
	"github.com/hashicorp/consul/api"
)

const timeout = "30s"

type Client struct {
	*api.Client
}

func NewClient(addr string) (*Client, error) {
	conf := &api.Config{
		Address: addr,
	}

	client, err := api.NewClient(conf)
	if err != nil {
		return nil, fmt.Errorf("error to create consul client: %w", err)
	}

	return &Client{client}, nil
}

type GrpcRegConf struct {
	Id, Name, Addr string
	Port           int
	Tags           []string
	Interval       int
	TLS            bool
}

func (c *Client) GrpcRegistration(conf *GrpcRegConf) error {
	registration := &api.AgentServiceRegistration{
		ID:   conf.Id,
		Name: conf.Name,
		Port: conf.Port,
		Tags: conf.Tags,
		Check: &api.AgentServiceCheck{
			GRPC:       fmt.Sprintf("%s:%d", conf.Addr, conf.Port),
			GRPCUseTLS: conf.TLS,
			Interval:   fmt.Sprintf("%ds", conf.Interval),
			Timeout:    timeout,
		},
	}
	err := c.Agent().ServiceRegister(registration)
	if err != nil {
		return fmt.Errorf("error to register grpc client: %w", err)
	}

	return nil
}
