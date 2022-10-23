package rpc_handler

import (
	"context"
	"fmt"
	"github.com/evgeniy-krivenko/outline-ss-server/server"
	"github.com/evgeniy-krivenko/vpn-api/gen/ss_service"
	"net"
	"time"
)

var cipherType = "chacha20-ietf-poly1305"

type Handler struct {
	ss *server.SSServer
	ss_service.UnimplementedSsServiceServer
}

func NewGrpcHandler(ss *server.SSServer) *Handler {
	return &Handler{ss: ss}
}

func (h *Handler) ActivateSsConnection(ctx context.Context, acr *ss_service.SsConnectionReq) (*ss_service.SsConnectionRes, error) {
	_, err := h.ss.AddCipher(server.CipherStruct{
		Port:   int(acr.GetPort()),
		ID:     acr.GetUserId(),
		Secret: acr.GetSecret(),
		Cipher: cipherType,
	})
	if err != nil {
		return nil, err
	}

	return &ss_service.SsConnectionRes{IsActive: true}, nil
}
func (h *Handler) DeactivateSsConnection(ctx context.Context, req *ss_service.SsConnectionReq) (*ss_service.SsConnectionRes, error) {
	err := h.ss.RemoveCipher(server.CipherStruct{
		Port: int(req.GetPort()),
		ID:   req.GetUserId(),
	})
	if err != nil {
		return nil, err
	}
	return &ss_service.SsConnectionRes{IsActive: false}, nil
}
func (h *Handler) SsConnectionStatus(ctx context.Context, req *ss_service.SsConnectionReq) (*ss_service.SsConnectionRes, error) {
	isActive := h.ss.IsCipherExists(server.CipherStruct{
		Port: int(req.GetPort()),
		ID:   req.UserId,
	})
	return &ss_service.SsConnectionRes{IsActive: isActive}, nil
}
func (h *Handler) CheckSsPortAvailable(ctx context.Context, req *ss_service.CheckSsPortAvailableReq) (*ss_service.CheckSsPortAvailableRes, error) {
	var resp ss_service.CheckSsPortAvailableRes
	timeout := time.Second
	conn, err := net.DialTimeout(
		"tcp",
		fmt.Sprintf(":%d", req.Port),
		timeout,
	)

	if err != nil {
		fmt.Println("Connecting error:", err)
		return &resp, nil
	}

	if conn != nil {
		defer conn.Close()
		resp.Status = true
		return &resp, nil
	}
	
	return nil, fmt.Errorf("error while check ss port: conn is equal nil")
}
