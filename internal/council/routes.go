package council

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/state"
	"github.com/google/uuid"
)

const tokenBytes = 20

var (
	// ErrHostRequired is returned when the host field is empty.
	ErrHostRequired = errors.New("host field cannot be null or undefined")

	// ErrLocationRequired is returned when the location field is empty.
	ErrLocationRequired = errors.New("location field cannot be null or undefined")

	// ErrLingIDRequired is returned when the ling_id field is empty.
	ErrLingIDRequired = errors.New("ling_id field cannot be null or undefined")

	// ErrPreferredLocationRequired is returned when the preferred_location field is empty.
	ErrPreferredLocationRequired = errors.New(
		"preferred_location field cannot be null or undefined",
	)
)

// PutKingRequest is the JSON request body for the PUT /king endpoint.
type PutKingRequest struct {
	Host            string          `json:"host"`
	ShuttingDown    bool            `json:"shuttingDown"`
	Tunnels         []PutKingTunnel `json:"tunnels"`
	ReadyServiceIDs []string        `json:"readyServiceIds"`
	Location        string          `json:"location"`
	CertPEM         string          `json:"certPem"`
}

// PutKingTunnel represents a tunnel in a king request.
type PutKingTunnel struct {
	BindPort int    `json:"bindPort"`
	Ports    string `json:"ports"`
}

// PutLingRequest is the JSON request body for the PUT /ling endpoint.
type PutLingRequest struct {
	LingID            string          `json:"lingId"`
	ShuttingDown      bool            `json:"shuttingDown"`
	Tunnels           []PutLingTunnel `json:"tunnels"`
	ReadyServiceIDs   []string        `json:"readyServiceIds"`
	PreferredLocation string          `json:"preferredLocation"`
}

// PutLingTunnel represents a tunnel in a ling request, carrying the identity the ling already holds for it.
type PutLingTunnel struct {
	Name       string  `json:"name"`
	ServiceID  string  `json:"serviceId,omitempty"`
	Token      string  `json:"token,omitempty"`
	Host       *string `json:"host,omitempty"`
	BindPort   *int    `json:"bindPort,omitempty"`
	RemotePort *int    `json:"remotePort,omitempty"`
}

// HandleGetState returns an HTTP handler that responds with the current state as JSON.
func HandleGetState(
	currentState *state.State,
	stateMutex *sync.RWMutex,
	warmup *Warmup,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		if warmup.active() {
			http.Error(writer, "council is warming up", http.StatusServiceUnavailable)

			return
		}

		stateMutex.RLock()
		defer stateMutex.RUnlock()

		writer.Header().Set("Content-Type", "application/json; charset=utf-8")

		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")

		err := encoder.Encode(currentState)
		if err != nil {
			slog.Error("Failed to encode state", "error", err)
		}
	}
}

// HandlePutKing returns an HTTP handler for king registration/heartbeat.
func HandlePutKing(
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	warmup *Warmup,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var req PutKingRequest

		err := decodeRequest(request, &req)
		if err == nil {
			err = validatePutKingRequest(&req)
		}

		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)

			return
		}

		stateMutex.Lock()
		changed := applyKingTunnels(currentState, &req)
		changed = markReady(
			currentState,
			req.ReadyServiceIDs,
			func(svc *state.Service) *bool { return &svc.KingReady },
		) ||
			changed
		commitChange(request.Context(), currentState, stateMutex, hub, warmup, changed)

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(writer, "ok")
	}
}

// HandlePutLing returns an HTTP handler for ling registration/heartbeat.
func HandlePutLing(
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	warmup *Warmup,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var req PutLingRequest

		err := decodeRequest(request, &req)
		if err == nil {
			err = validatePutLingRequest(&req)
		}

		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)

			return
		}

		stateMutex.Lock()
		changed := applyLing(currentState, &req)
		changed = applyLingTunnels(currentState, &req, warmup) || changed
		changed = markReady(
			currentState,
			req.ReadyServiceIDs,
			func(svc *state.Service) *bool { return &svc.LingReady },
		) ||
			changed
		commitChange(request.Context(), currentState, stateMutex, hub, warmup, changed)

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(writer, "ok")
	}
}

func decodeRequest(request *http.Request, target any) error {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}

	err = json.Unmarshal(body, target)
	if err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}

	return nil
}

func commitChange(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	warmup *Warmup,
	changed bool,
) {
	publish := changed && !warmup.active()
	if publish {
		Provision(currentState)
	}

	if changed {
		currentState.Revision++
	}

	stateMutex.Unlock()

	if publish {
		hub.Broadcast(ctx)
	}
}

func validatePutKingRequest(req *PutKingRequest) error {
	if req.Host == "" {
		return ErrHostRequired
	}

	if req.Location == "" {
		return ErrLocationRequired
	}

	return nil
}

func validatePutLingRequest(req *PutLingRequest) error {
	if req.LingID == "" {
		return ErrLingIDRequired
	}

	if req.PreferredLocation == "" {
		return ErrPreferredLocationRequired
	}

	return nil
}

func markReady(
	currentState *state.State,
	serviceIDs []string,
	readyFlag func(*state.Service) *bool,
) bool {
	changed := false

	for _, serviceID := range serviceIDs {
		svc := FindService(currentState, serviceID)
		if svc == nil || *readyFlag(svc) {
			continue
		}

		*readyFlag(svc) = true
		changed = true
	}

	return changed
}

func applyKingTunnels(currentState *state.State, req *PutKingRequest) bool {
	changed := false
	now := currentTimeMillis()

	for _, tunnel := range req.Tunnels {
		king := FindKing(currentState, tunnel.Ports, req.Host)
		if king == nil {
			currentState.Kings = append(currentState.Kings, state.King{
				BindPort:     tunnel.BindPort,
				Ports:        tunnel.Ports,
				Host:         req.Host,
				ShuttingDown: req.ShuttingDown,
				Beat:         now,
				Location:     req.Location,
				CertPEM:      req.CertPEM,
			})
			changed = true

			continue
		}

		if king.ShuttingDown != req.ShuttingDown || king.CertPEM != req.CertPEM {
			king.ShuttingDown = req.ShuttingDown
			king.CertPEM = req.CertPEM
			changed = true
		}

		king.Beat = now
	}

	return changed
}

func applyLing(currentState *state.State, req *PutLingRequest) bool {
	now := currentTimeMillis()

	ling := FindLing(currentState, req.LingID)
	if ling == nil {
		currentState.Lings = append(currentState.Lings, state.Ling{
			LingID:       req.LingID,
			ShuttingDown: false,
			Beat:         now,
		})
		ling = &currentState.Lings[len(currentState.Lings)-1]
	}

	ling.Beat = now

	if ling.ShuttingDown == req.ShuttingDown {
		return false
	}

	ling.ShuttingDown = req.ShuttingDown

	for i := range currentState.Services {
		if req.ShuttingDown && currentState.Services[i].LingID == req.LingID {
			currentState.Services[i].LingReady = false
		}
	}

	return true
}

func applyLingTunnels(currentState *state.State, req *PutLingRequest, warmup *Warmup) bool {
	changed := false

	for _, tunnel := range req.Tunnels {
		if FindServiceByNameAndLing(currentState, tunnel.Name, req.LingID) != nil {
			continue
		}

		svc := state.Service{
			ServiceID:         uuid.New().String(),
			Name:              tunnel.Name,
			Token:             GenerateToken(),
			PreferredLocation: req.PreferredLocation,
			LingID:            req.LingID,
			LingReady:         false,
			KingReady:         false,
			Host:              nil,
			BindPort:          nil,
			RemotePort:        nil,
		}

		if tunnel.ServiceID != "" && tunnel.Token != "" &&
			FindService(currentState, tunnel.ServiceID) == nil {
			svc.ServiceID = tunnel.ServiceID
			svc.Token = tunnel.Token

			if assignmentAvailable(currentState, tunnel, warmup) {
				svc.Host = tunnel.Host
				svc.BindPort = tunnel.BindPort
				svc.RemotePort = tunnel.RemotePort
			}

			slog.Info(
				"Adopted service from ling",
				"name",
				svc.Name,
				"service_id",
				svc.ServiceID,
				"assigned",
				svc.BindPort != nil,
			)
		}

		currentState.Services = append(currentState.Services, svc)
		changed = true
	}

	return changed
}

func assignmentAvailable(currentState *state.State, tunnel PutLingTunnel, warmup *Warmup) bool {
	if tunnel.Host == nil || tunnel.BindPort == nil || tunnel.RemotePort == nil {
		return false
	}

	assignedKing := state.King{
		BindPort:     *tunnel.BindPort,
		Host:         *tunnel.Host,
		Ports:        "",
		ShuttingDown: false,
		Beat:         0,
		Location:     "",
		CertPEM:      "",
	}
	if collectUsedPorts(&assignedKing, currentState)[*tunnel.RemotePort] {
		return false
	}

	for _, king := range currentState.Kings {
		if king.Host == assignedKing.Host && king.BindPort == assignedKing.BindPort {
			return !king.ShuttingDown
		}
	}

	return warmup.active()
}

// FindService looks up a service by ID in the current state.
func FindService(
	currentState *state.State,
	serviceID string,
) *state.Service {
	for i := range currentState.Services {
		if currentState.Services[i].ServiceID == serviceID {
			return &currentState.Services[i]
		}
	}

	return nil
}

// FindServiceByNameAndLing looks up a service by name and ling ID.
func FindServiceByNameAndLing(
	currentState *state.State,
	name, lingID string,
) *state.Service {
	for i := range currentState.Services {
		if currentState.Services[i].Name == name &&
			currentState.Services[i].LingID == lingID {
			return &currentState.Services[i]
		}
	}

	return nil
}

// FindKing looks up a king by ports and host.
func FindKing(
	currentState *state.State,
	ports, host string,
) *state.King {
	for i := range currentState.Kings {
		if currentState.Kings[i].Ports == ports &&
			currentState.Kings[i].Host == host {
			return &currentState.Kings[i]
		}
	}

	return nil
}

// FindLing looks up a ling by its ID.
func FindLing(
	currentState *state.State,
	lingID string,
) *state.Ling {
	for i := range currentState.Lings {
		if currentState.Lings[i].LingID == lingID {
			return &currentState.Lings[i]
		}
	}

	return nil
}

// GenerateToken generates a random hex token for service authentication.
func GenerateToken() string {
	tokenBuffer := make([]byte, tokenBytes)

	_, err := rand.Read(tokenBuffer)
	if err != nil {
		slog.Error("Failed to generate random token", "error", err)
	}

	return hex.EncodeToString(tokenBuffer)
}

func currentTimeMillis() int64 {
	return time.Now().UnixMilli()
}
