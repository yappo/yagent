package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

type durableWorkBatch struct {
	Snapshot    domain.WorkflowSnapshot
	Units       []domain.DurableWorkUnit
	Credentials map[domain.DurableWorkUnitID]domain.LeaseCredential
}

func (s *Service) claimAndStartDurableBatch(ctx context.Context, workflowID domain.WorkflowID, at time.Time) (durableWorkBatch, error) {
	if s.config.WorkflowStore == nil {
		return durableWorkBatch{}, fmt.Errorf("durable workflow store is required")
	}
	if at.IsZero() {
		return durableWorkBatch{}, fmt.Errorf("durable batch time is required")
	}
	snapshot, err := s.propagateBlockedWorkUnits(ctx, workflowID, at)
	if err != nil {
		return durableWorkBatch{}, err
	}
	snapshot, err = s.reconcileExpiredDurableWorkUnits(ctx, snapshot, at)
	if err != nil {
		return durableWorkBatch{}, err
	}
	if hasActiveDurableWorkUnits(snapshot.WorkUnits) {
		return durableWorkBatch{}, fmt.Errorf("workflow %q is owned by another worker with an active lease", workflowID)
	}
	unitIDs := selectReadyDurableBatch(snapshot, s.config.MaxParallelAgents)
	if len(unitIDs) == 0 {
		return durableWorkBatch{Snapshot: snapshot}, nil
	}

	claims := make([]domain.WorkUnitClaim, 0, len(unitIDs))
	credentials := make(map[domain.DurableWorkUnitID]domain.LeaseCredential, len(unitIDs))
	for _, unitID := range unitIDs {
		unit, ok := durableSnapshotUnit(snapshot, unitID)
		if !ok {
			return durableWorkBatch{}, fmt.Errorf("ready work unit %q disappeared", unitID)
		}
		credential := domain.LeaseCredential{Token: domain.LeaseToken(s.nextRunID("lease")), FencingToken: unit.LastFencingToken + 1}
		credentials[unitID] = credential
		claims = append(claims, domain.WorkUnitClaim{
			UnitID: unitID,
			Lease: domain.DurableLease{
				OwnerID: s.config.WorkerID, Token: credential.Token, FencingToken: credential.FencingToken,
				ExpiresAt: at.Add(s.config.WorkflowLeaseDuration),
			},
		})
	}

	_, err = s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.ClaimReadyBatch(current, domain.WorkflowBatchClaims{ExpectedRevision: current.Workflow.Revision, Claims: claims}, at)
	}, durableClaimsApplied(claims))
	if err != nil {
		return durableWorkBatch{}, err
	}
	started, err := s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		items := make([]domain.WorkUnitCredential, 0, len(unitIDs))
		for _, unitID := range unitIDs {
			items = append(items, domain.WorkUnitCredential{UnitID: unitID, Credential: credentials[unitID]})
		}
		return domain.StartClaimedBatch(current, domain.WorkflowBatchCredentials{ExpectedRevision: current.Workflow.Revision, Credentials: items}, at)
	}, durableStartsApplied(credentials))
	if err != nil {
		return durableWorkBatch{}, err
	}

	batch := durableWorkBatch{Snapshot: started, Credentials: credentials}
	for _, unitID := range unitIDs {
		unit, _ := durableSnapshotUnit(started, unitID)
		batch.Units = append(batch.Units, unit)
	}
	return batch, nil
}

func (s *Service) reconcileExpiredDurableWorkUnits(ctx context.Context, snapshot domain.WorkflowSnapshot, at time.Time) (domain.WorkflowSnapshot, error) {
	if !hasExpiredDurableWorkUnits(snapshot.WorkUnits, at) {
		return snapshot, nil
	}
	workflowID := snapshot.Workflow.ID
	return s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		transitionAt := at
		if transitionAt.Before(current.Workflow.UpdatedAt) {
			transitionAt = current.Workflow.UpdatedAt
		}
		return domain.ReconcileExpiredLeases(current, domain.ReconcileExpiredLeasesInput{ExpectedRevision: current.Workflow.Revision, At: transitionAt})
	}, func(current domain.WorkflowSnapshot) bool {
		return !hasExpiredDurableWorkUnits(current.WorkUnits, at)
	})
}

func (s *Service) renewDurableWorkBatch(ctx context.Context, workflowID domain.WorkflowID, credentials map[domain.DurableWorkUnitID]domain.LeaseCredential, at time.Time) error {
	if len(credentials) == 0 {
		return nil
	}
	expiresAt := at.Add(s.config.WorkflowLeaseDuration)
	_, err := s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		renewals := make([]domain.WorkUnitLeaseRenewal, 0, len(credentials))
		for unitID, credential := range credentials {
			unit, ok := durableSnapshotUnit(current, unitID)
			if !ok || unit.Lease == nil || (unit.Status != domain.DurableWorkUnitStatusLeased && unit.Status != domain.DurableWorkUnitStatusExecuting) {
				return domain.WorkflowSnapshot{}, fmt.Errorf("%w: durable work unit %q is no longer active", domain.ErrLeaseMismatch, unitID)
			}
			renewals = append(renewals, domain.WorkUnitLeaseRenewal{UnitID: unitID, Credential: credential, ExpiresAt: expiresAt})
		}
		transitionAt := at
		if transitionAt.Before(current.Workflow.UpdatedAt) {
			transitionAt = current.Workflow.UpdatedAt
		}
		return domain.RenewWorkUnitLeases(current, domain.RenewWorkUnitLeasesInput{ExpectedRevision: current.Workflow.Revision, Renewals: renewals, At: transitionAt})
	}, durableRenewalsApplied(credentials, expiresAt))
	return err
}

func (s *Service) propagateBlockedWorkUnits(ctx context.Context, workflowID domain.WorkflowID, at time.Time) (domain.WorkflowSnapshot, error) {
	snapshot, err := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, workflowID)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	blocks := blockingWorkUnitCommands(snapshot)
	if len(blocks) == 0 {
		return snapshot, nil
	}
	return s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		currentBlocks := blockingWorkUnitCommands(current)
		if len(currentBlocks) == 0 {
			return current, nil
		}
		return domain.BlockWorkUnits(current, domain.BlockWorkUnitsInput{ExpectedRevision: current.Workflow.Revision, Blocks: currentBlocks, At: at})
	}, durableBlocksApplied(blockUnitIDs(blocks)))
}

func blockingWorkUnitCommands(snapshot domain.WorkflowSnapshot) []domain.WorkUnitBlock {
	statuses := make(map[domain.DurableWorkUnitID]domain.DurableWorkUnitStatus, len(snapshot.WorkUnits))
	for _, unit := range snapshot.WorkUnits {
		statuses[unit.ID] = unit.Status
	}
	blocks := make([]domain.WorkUnitBlock, 0)
	for {
		added := false
		for _, unit := range snapshot.WorkUnits {
			if statuses[unit.ID] != domain.DurableWorkUnitStatusPending {
				continue
			}
			blocking := make([]string, 0)
			for _, dependencyID := range unit.Dependencies {
				status := statuses[dependencyID]
				if status == domain.DurableWorkUnitStatusFailed || status == domain.DurableWorkUnitStatusNeedsAttention || status == domain.DurableWorkUnitStatusBlocked {
					blocking = append(blocking, string(dependencyID))
				}
			}
			if len(blocking) == 0 {
				continue
			}
			blocks = append(blocks, domain.WorkUnitBlock{UnitID: unit.ID, Reason: "blocked by failed dependency: " + strings.Join(blocking, ", ")})
			statuses[unit.ID] = domain.DurableWorkUnitStatusBlocked
			added = true
		}
		if !added {
			return blocks
		}
	}
}

func selectReadyDurableBatch(snapshot domain.WorkflowSnapshot, maxParallel int) []domain.DurableWorkUnitID {
	completed := make(map[string]bool, len(snapshot.WorkUnits))
	pending := make([]domain.DurableWorkUnit, 0, len(snapshot.WorkUnits))
	for _, unit := range snapshot.WorkUnits {
		if unit.Status == domain.DurableWorkUnitStatusSucceeded || unit.Status == domain.DurableWorkUnitStatusSkipped {
			completed[string(unit.ID)] = true
		}
		if unit.Status == domain.DurableWorkUnitStatusPending {
			pending = append(pending, unit)
		}
	}
	specs := make([]scheduleSpec, 0, len(pending))
	for _, unit := range pending {
		specs = append(specs, scheduleSpec{
			ID: string(unit.ID), DependsOn: stringDependencyIDs(unit.Dependencies), ReadSet: append([]string(nil), unit.ReadSet...),
			WriteSet: append([]string(nil), unit.WriteSet...), SideEffectClass: unit.SideEffectClass,
			DuplicateKey: unit.DuplicateKey, Source: unit.Source, SourceLimit: unit.SourceLimit,
		})
	}
	indexes := newRuntimeScheduler(maxParallel).nextBatch(specs, completed)
	unitIDs := make([]domain.DurableWorkUnitID, 0, len(indexes))
	for _, index := range indexes {
		unitIDs = append(unitIDs, pending[index].ID)
	}
	return unitIDs
}

func durableClaimsApplied(claims []domain.WorkUnitClaim) workflowCommandApplied {
	return func(snapshot domain.WorkflowSnapshot) bool {
		for _, claim := range claims {
			unit, ok := durableSnapshotUnit(snapshot, claim.UnitID)
			if !ok || unit.Lease == nil || unit.Lease.Token != claim.Lease.Token || unit.Lease.FencingToken != claim.Lease.FencingToken {
				return false
			}
			if unit.Status != domain.DurableWorkUnitStatusLeased && unit.Status != domain.DurableWorkUnitStatusExecuting {
				return false
			}
		}
		return true
	}
}

func durableStartsApplied(credentials map[domain.DurableWorkUnitID]domain.LeaseCredential) workflowCommandApplied {
	return func(snapshot domain.WorkflowSnapshot) bool {
		for unitID, credential := range credentials {
			unit, ok := durableSnapshotUnit(snapshot, unitID)
			if !ok || unit.Status != domain.DurableWorkUnitStatusExecuting || unit.Lease == nil || unit.Lease.Token != credential.Token || unit.Lease.FencingToken != credential.FencingToken {
				return false
			}
		}
		return true
	}
}

func durableBlocksApplied(unitIDs []domain.DurableWorkUnitID) workflowCommandApplied {
	return func(snapshot domain.WorkflowSnapshot) bool {
		for _, unitID := range unitIDs {
			unit, ok := durableSnapshotUnit(snapshot, unitID)
			if !ok || unit.Status != domain.DurableWorkUnitStatusBlocked {
				return false
			}
		}
		return true
	}
}

func durableRenewalsApplied(credentials map[domain.DurableWorkUnitID]domain.LeaseCredential, expiresAt time.Time) workflowCommandApplied {
	return func(snapshot domain.WorkflowSnapshot) bool {
		for unitID, credential := range credentials {
			unit, ok := durableSnapshotUnit(snapshot, unitID)
			if !ok || unit.Lease == nil || unit.Lease.Token != credential.Token || unit.Lease.FencingToken != credential.FencingToken || unit.Lease.ExpiresAt.Before(expiresAt) {
				return false
			}
		}
		return true
	}
}

func blockUnitIDs(blocks []domain.WorkUnitBlock) []domain.DurableWorkUnitID {
	ids := make([]domain.DurableWorkUnitID, 0, len(blocks))
	for _, block := range blocks {
		ids = append(ids, block.UnitID)
	}
	return ids
}

func durableSnapshotUnit(snapshot domain.WorkflowSnapshot, unitID domain.DurableWorkUnitID) (domain.DurableWorkUnit, bool) {
	for _, unit := range snapshot.WorkUnits {
		if unit.ID == unitID {
			return unit, true
		}
	}
	return domain.DurableWorkUnit{}, false
}

func hasActiveDurableWorkUnits(units []domain.DurableWorkUnit) bool {
	for _, unit := range units {
		if unit.Status == domain.DurableWorkUnitStatusLeased || unit.Status == domain.DurableWorkUnitStatusExecuting {
			return true
		}
	}
	return false
}

func hasExpiredDurableWorkUnits(units []domain.DurableWorkUnit, at time.Time) bool {
	for _, unit := range units {
		if unit.Lease != nil && (unit.Status == domain.DurableWorkUnitStatusLeased || unit.Status == domain.DurableWorkUnitStatusExecuting) && !unit.Lease.ExpiresAt.After(at) {
			return true
		}
	}
	return false
}
