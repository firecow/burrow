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
	// ErrServiceNotFound is returned when a service ID cannot be found in state.
	ErrServiceNotFound = errors.New("cannot be found in state.services")

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

	// ErrServiceUndefined is returned when a referenced service is undefined.
	ErrServiceUndefined = errors.New("service is undefined or null")
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

// PutLingTunnel represents a tunnel in a ling request.
type PutLingTunnel struct {
	Name string `json:"name"`
}

// HandleGetState returns an HTTP handler that responds with the current state as JSON.
func HandleGetState(
	currentState *state.State,
	stateMutex *sync.RWMutex,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
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
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "failed to read body", http.StatusBadRequest)

			return
		}

		var req PutKingRequest

		err = json.Unmarshal(body, &req)
		if err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)

			return
		}

		err = validatePutKingRequest(&req)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)

			return
		}

		stateMutex.Lock()

		kingReadyErr := processKingReadyServices(
			request.Context(), currentState, stateMutex, hub,
			req.ReadyServiceIDs, writer,
		)
		if kingReadyErr {
			return
		}

		processKingTunnels(
			request.Context(), currentState, stateMutex, hub, &req,
		)
		stateMutex.Unlock()

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(writer, "ok")
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

func processKingReadyServices(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	readyServiceIDs []string,
	writer http.ResponseWriter,
) bool {
	for _, serviceID := range readyServiceIDs {
		svc := FindService(currentState, serviceID)
		if svc == nil {
			stateMutex.Unlock()

			http.Error(
				writer,
				serviceID+" cannot be found in state.services",
				http.StatusBadRequest,
			)

			return true
		}

		if !svc.KingReady {
			svc.KingReady = true
			currentState.Revision++
			Provision(currentState)
			stateMutex.Unlock()
			hub.Broadcast(ctx)
			stateMutex.Lock()
		}
	}

	return false
}

func processKingTunnels(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	req *PutKingRequest,
) {
	now := currentTimeMillis()

	for _, tunnel := range req.Tunnels {
		existingKing := FindKing(currentState, tunnel.Ports, req.Host)
		if existingKing != nil {
			updateExistingKing(
				ctx, currentState, stateMutex, hub, existingKing, req, now,
			)

			continue
		}

		currentState.Kings = append(currentState.Kings, state.King{
			BindPort:     tunnel.BindPort,
			Ports:        tunnel.Ports,
			Host:         req.Host,
			ShuttingDown: false,
			Beat:         now,
			Location:     req.Location,
			CertPEM:      req.CertPEM,
		})
		currentState.Revision++
		Provision(currentState)
		stateMutex.Unlock()
		hub.Broadcast(ctx)
		stateMutex.Lock()
	}
}

func updateExistingKing(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	existingKing *state.King,
	req *PutKingRequest,
	now int64,
) {
	changed := existingKing.ShuttingDown != req.ShuttingDown ||
		existingKing.CertPEM != req.CertPEM
	existingKing.ShuttingDown = req.ShuttingDown
	existingKing.CertPEM = req.CertPEM

	if changed {
		currentState.Revision++
		Provision(currentState)
		stateMutex.Unlock()
		hub.Broadcast(ctx)
		stateMutex.Lock()
	}

	existingKing.Beat = now
}

// HandlePutLing returns an HTTP handler for ling registration/heartbeat.
func HandlePutLing(
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "failed to read body", http.StatusBadRequest)

			return
		}

		var req PutLingRequest

		err = json.Unmarshal(body, &req)
		if err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)

			return
		}

		err = validatePutLingRequest(&req)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)

			return
		}

		stateMutex.Lock()

		lingReadyErr := processLingReadyServices(
			request.Context(), currentState, stateMutex, hub,
			req.ReadyServiceIDs, writer,
		)
		if lingReadyErr {
			return
		}

		earlyReturn := processLingTunnels(
			request.Context(), currentState, stateMutex, hub, writer, &req,
		)
		if earlyReturn {
			return
		}

		stateMutex.Unlock()

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(writer, "ok")
	}
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

func processLingReadyServices(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	readyServiceIDs []string,
	writer http.ResponseWriter,
) bool {
	for _, serviceID := range readyServiceIDs {
		svc := FindService(currentState, serviceID)
		if svc == nil {
			stateMutex.Unlock()

			http.Error(
				writer,
				ErrServiceUndefined.Error(),
				http.StatusBadRequest,
			)

			return true
		}

		if !svc.LingReady {
			svc.LingReady = true
			currentState.Revision++
			Provision(currentState)
			stateMutex.Unlock()
			hub.Broadcast(ctx)
			stateMutex.Lock()
		}
	}

	return false
}

func processLingTunnels(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	writer http.ResponseWriter,
	req *PutLingRequest,
) bool {
	now := currentTimeMillis()

	for _, tunnel := range req.Tunnels {
		earlyReturn := processOneLingTunnel(
			ctx, currentState, stateMutex, hub, writer, req, tunnel, now,
		)
		if earlyReturn {
			return true
		}
	}

	return false
}

func ensureLingExists(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	req *PutLingRequest,
	now int64,
) {
	existingLing := FindLing(currentState, req.LingID)
	if existingLing == nil {
		currentState.Lings = append(currentState.Lings, state.Ling{
			LingID:       req.LingID,
			ShuttingDown: req.ShuttingDown,
			Beat:         now,
		})
		existingLing = &currentState.Lings[len(currentState.Lings)-1]
	}

	existingLing.Beat = now

	if existingLing.ShuttingDown != req.ShuttingDown {
		existingLing.ShuttingDown = req.ShuttingDown

		for i := range currentState.Services {
			if req.ShuttingDown && currentState.Services[i].LingID == req.LingID {
				currentState.Services[i].LingReady = false
			}
		}

		currentState.Revision++
		Provision(currentState)
		stateMutex.Unlock()
		hub.Broadcast(ctx)
		stateMutex.Lock()
	}
}

func processOneLingTunnel(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	writer http.ResponseWriter,
	req *PutLingRequest,
	tunnel PutLingTunnel,
	now int64,
) bool {
	ensureLingExists(ctx, currentState, stateMutex, hub, req, now)

	existingService := FindServiceByNameAndLing(
		currentState, tunnel.Name, req.LingID,
	)
	if existingService != nil {
		stateMutex.Unlock()
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(writer, "ok")

		return true
	}

	token := GenerateToken()
	currentState.Services = append(currentState.Services, state.Service{
		ServiceID:         uuid.New().String(),
		Name:              tunnel.Name,
		Token:             token,
		PreferredLocation: req.PreferredLocation,
		LingID:            req.LingID,
		LingReady:         false,
		KingReady:         false,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	})
	currentState.Revision++
	Provision(currentState)
	stateMutex.Unlock()
	hub.Broadcast(ctx)
	stateMutex.Lock()

	return false
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
