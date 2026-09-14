package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/allocation"
	"resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/llmloop"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/session"
	"resume-agent-kernel/internal/tools"
	"time"
)

type Service struct {
	allocation        *allocation.Service
	tasks             session.Store
	Build             string
	externalProviders []tools.Provider
	capabilities      p.KernelCapabilitiesV1
	capabilityErr     error
}

func NewService(build string, externalProviders ...tools.Provider) *Service {
	s := &Service{Build: build, externalProviders: externalProviders, allocation: allocation.New(build)}
	providers := append([]tools.Provider{&agent.Provider{}}, externalProviders...)
	registry, err := tools.NewProviders(providers...)
	s.capabilityErr = err
	if err == nil {
		fingerprint, err := registry.Fingerprint()
		s.capabilityErr = err
		s.capabilities = p.KernelCapabilitiesV1{
			ProtocolVersion: p.TaskProtocolVersion, ResultSchemaVersion: p.TaskResultVersion,
			TaskKinds: []string{p.ResumeJobMatchTaskKind}, KernelBuild: build,
			ToolsetVersion: fingerprint, InstructionVersion: llmloop.InstructionVersion(),
		}
	}
	return s
}

func (s *Service) Capabilities() (p.KernelCapabilitiesV1, error) {
	value := s.capabilities
	value.TaskKinds = append([]string(nil), value.TaskKinds...)
	return value, s.capabilityErr
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func (s *Service) AllocationCapabilities() contract.AllocationCapabilities {
	return s.allocation.Capabilities()
}
func (s *Service) ExecuteAllocation(ctx context.Context, r contract.AllocationRequest) (contract.AllocationResponse, error) {
	return s.allocation.Execute(ctx, r)
}
