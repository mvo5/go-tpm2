// Copyright 2023 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package policyutil

import (
	"bytes"
	"crypto"
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/go-tpm2"
	"github.com/canonical/go-tpm2/mu"
)

const (
	// policyOrMaxDigests sets a reasonable limit on the maximum number of or
	// digests.
	policyOrMaxDigests = 4096 // equivalent to a depth of 4
)

// ensureSufficientORDigests turns a single digest in to a pair of identical digests.
// This is because TPM2_PolicyOR assertions require more than one digest. This avoids
// having a separate policy sequence when there is only a single digest, without having
// to store duplicate digests on disk.
func ensureSufficientORDigests(digests tpm2.DigestList) tpm2.DigestList {
	if len(digests) == 1 {
		return tpm2.DigestList{digests[0], digests[0]}
	}
	return digests
}

type policyOrNode struct {
	parent  *policyOrNode
	digests tpm2.DigestList
}

type policyOrTree struct {
	alg       tpm2.HashAlgorithmId
	leafNodes []*policyOrNode
}

func newPolicyOrTree(alg tpm2.HashAlgorithmId, digests tpm2.DigestList) (out *policyOrTree, err error) {
	if len(digests) == 0 {
		return nil, errors.New("no digests")
	}
	if len(digests) > policyOrMaxDigests {
		return nil, errors.New("too many digests")
	}

	var prev []*policyOrNode

	for len(prev) != 1 {
		// The outer loop runs on each level of the tree. If
		// len(prev) == 1, then we have produced the root node
		// and the loop should not continue.

		var current []*policyOrNode
		var nextDigests tpm2.DigestList

		for len(digests) > 0 {
			// The inner loop runs on each sibling node within a level.

			n := len(digests)
			if n > 8 {
				// The TPM only supports 8 conditions in TPM2_PolicyOR.
				n = 8
			}

			// Create a new node with the next n digests and save it.
			node := &policyOrNode{digests: ensureSufficientORDigests(digests[:n])}
			current = append(current, node)

			// Consume the next n digests to fit in to this node and produce a single digest
			// that will go in to the parent node.
			trial := newComputePolicySession(&taggedHash{HashAlg: alg, Digest: make(tpm2.Digest, alg.Size())})
			trial.PolicyOR(node.digests)
			nextDigests = append(nextDigests, trial.digest.Digest)

			// We've consumed n digests, so adjust the slice to point to the next ones to consume to
			// produce a sibling node.
			digests = digests[n:]
		}

		// There are no digests left to produce sibling nodes.
		// Link child nodes to parents.
		for i, child := range prev {
			child.parent = current[i>>3]
		}

		// Grab the digests for the nodes we've just produced to create the parent nodes.
		prev = current
		digests = nextDigests

		if out == nil {
			// Save the leaf nodes to return.
			out = &policyOrTree{
				alg:       alg,
				leafNodes: current,
			}
		}
	}

	return out, nil
}

func (t *policyOrTree) selectBranch(i int) (out []tpm2.DigestList) {
	node := t.leafNodes[i>>3]

	for node != nil {
		out = append(out, ensureSufficientORDigests(node.digests))
		node = node.parent
	}

	return out
}

type policyBranchSelectMixin struct{}

func (*policyBranchSelectMixin) selectBranch(branches policyBranches, next policyBranchPath) (int, error) {
	switch {
	case strings.HasPrefix(string(next), "…"):
		return 0, fmt.Errorf("cannot select branch: invalid component \"%s\"", next)
	case next[0] == '$':
		// select branch by index
		var selected int
		if _, err := fmt.Sscanf(string(next), "$[%d]", &selected); err != nil {
			return 0, fmt.Errorf("cannot select branch: badly formatted path component \"%s\": %w", next, err)
		}
		if selected < 0 || selected >= len(branches) {
			return 0, fmt.Errorf("cannot select branch: selected path %d out of range", selected)
		}
		return selected, nil
	default:
		// select branch by name
		for i, branch := range branches {
			if len(branch.Name) == 0 {
				continue
			}
			if policyBranchPath(branch.Name) == next {
				return i, nil
			}
		}
		return 0, fmt.Errorf("cannot select branch: no branch with name \"%s\"", next)
	}
}

type policyBranchSelector struct {
	mockPolicyResourceLoader

	sessionAlg           tpm2.HashAlgorithmId
	resources            PolicyResourceLoader
	controller           policyRunnerController
	tpm                  TPMConnection
	subPolicyRunner      subPolicyRunner
	usage                *PolicySessionUsage
	ignoreAuthorizations []PolicyAuthorizationID
	ignoreNV             []Named

	paths      []policyBranchPath
	detailsMap map[policyBranchPath]PolicyBranchDetails
	nvOk       map[paramKey]struct{}
}

func newPolicyBranchSelector(sessionAlg tpm2.HashAlgorithmId, resources PolicyResourceLoader, controller policyRunnerController, subPolicyRunner subPolicyRunner, tpm TPMConnection, usage *PolicySessionUsage, ignoreAuthorizations []PolicyAuthorizationID, ignoreNV []Named) *policyBranchSelector {
	return &policyBranchSelector{
		sessionAlg:           sessionAlg,
		resources:            resources,
		controller:           controller,
		tpm:                  tpm,
		subPolicyRunner:      subPolicyRunner,
		usage:                usage,
		ignoreAuthorizations: ignoreAuthorizations,
		ignoreNV:             ignoreNV,
	}
}

func (s *policyBranchSelector) filterInvalidBranches() {
	for p, d := range s.detailsMap {
		if d.IsValid() {
			continue
		}
		delete(s.detailsMap, p)
	}
}

func (s *policyBranchSelector) filterMissingResourceBranches() {
	if s.resources != nil {
		return
	}

	for p, d := range s.detailsMap {
		if len(d.NV) > 0 || len(d.Secret) > 0 || len(d.Signed) > 0 || len(d.Authorize) > 0 {
			delete(s.detailsMap, p)
		}
	}
}

func (s *policyBranchSelector) filterMissingAuthBranches() {
	for p, d := range s.detailsMap {
		for _, auth := range d.Authorize {
			policies, err := s.resources.LoadAuthorizedPolicies(auth.AuthName, auth.PolicyRef)
			if err != nil || len(policies) == 0 {
				delete(s.detailsMap, p)
				break
			}
		}
	}
}

func (s *policyBranchSelector) filterIgnoredResources() {
	for _, ignore := range s.ignoreAuthorizations {
		for p, d := range s.detailsMap {
			found := false

			var auths []PolicyAuthorizationID
			auths = append(auths, d.Secret...)
			auths = append(auths, d.Signed...)
			auths = append(auths, d.Authorize...)

			for _, auth := range auths {
				if bytes.Equal(auth.AuthName, ignore.AuthName) && bytes.Equal(auth.PolicyRef, ignore.PolicyRef) {
					found = true
					break
				}
			}

			if found {
				delete(s.detailsMap, p)
			}
		}
	}

	for _, ignore := range s.ignoreNV {
		for p, d := range s.detailsMap {
			for _, nv := range d.NV {
				if bytes.Equal(nv.Name, ignore.Name()) {
					delete(s.detailsMap, p)
					break
				}
			}
		}
	}
}

func (s *policyBranchSelector) filterUsageIncompatibleBranches() error {
	if s.usage == nil {
		return nil
	}

	for p, d := range s.detailsMap {
		code, set := d.CommandCode()
		if set && code != s.usage.commandCode {
			delete(s.detailsMap, p)
			continue
		}

		cpHash, set := d.CpHash()
		if set {
			usageCpHash, err := ComputeCpHash(s.sessionAlg, s.usage.commandCode, s.usage.handles, s.usage.params...)
			if err != nil {
				return fmt.Errorf("cannot obtain cpHash from usage parameters: %w", err)
			}
			if !bytes.Equal(usageCpHash, cpHash) {
				delete(s.detailsMap, p)
				continue
			}
		}

		nameHash, set := d.NameHash()
		if set {
			usageNameHash, err := ComputeNameHash(s.sessionAlg, s.usage.handles...)
			if err != nil {
				return fmt.Errorf("cannot obtain nameHash from usage parameters: %w", err)
			}
			if !bytes.Equal(usageNameHash, nameHash) {
				delete(s.detailsMap, p)
				continue
			}
		}

		if d.AuthValueNeeded && s.usage.noAuthValue {
			delete(s.detailsMap, p)
			continue
		}

		nvWritten, set := d.NvWritten()
		if set && s.usage.nvHandle.Type() == tpm2.HandleTypeNVIndex {
			pub, err := s.tpm.NVReadPublic(tpm2.NewLimitedHandleContext(s.usage.nvHandle))
			if err != nil {
				return fmt.Errorf("cannot obtain NV index public area: %w", err)
			}
			written := pub.Attrs&tpm2.AttrNVWritten != 0
			if nvWritten != written {
				delete(s.detailsMap, p)
				continue
			}
		}
	}

	return nil
}

func (s *policyBranchSelector) filterPcrIncompatibleBranches() error {
	var pcrs tpm2.PCRSelectionList
	for p, d := range s.detailsMap {
		for _, item := range d.PCR {
			tmpPcrs, err := pcrs.Merge(item.PCRs)
			if err != nil {
				delete(s.detailsMap, p)
				break
			}
			pcrs = tmpPcrs
		}
	}

	if pcrs.IsEmpty() {
		return nil
	}

	pcrValues, err := s.tpm.PCRRead(pcrs)
	if err != nil {
		return fmt.Errorf("cannot obtain PCR values: %w", err)
	}

	for p, d := range s.detailsMap {
		for _, item := range d.PCR {
			pcrDigest, err := ComputePCRDigest(s.sessionAlg, item.PCRs, pcrValues)
			if err != nil {
				return fmt.Errorf("cannot compute PCR digest: %w", err)
			}
			if !bytes.Equal(pcrDigest, item.PCRDigest) {
				delete(s.detailsMap, p)
				break
			}
		}
	}

	return nil
}

func (s *policyBranchSelector) bufferMatch(operandA, operandB tpm2.Operand, operation tpm2.ArithmeticOp) bool {
	if len(operandA) != len(operandB) {
		panic("mismatched operand sizes")
	}

	switch operation {
	case tpm2.OpEq:
		return bytes.Equal(operandA, operandB)
	case tpm2.OpNeq:
		return !bytes.Equal(operandA, operandB)
	case tpm2.OpSignedGT:
		switch {
		case len(operandA) == 0:
			return false
		case (operandA[0]^operandB[0])&0x80 > 0:
			return operandA[0]&0x80 == 0
		default:
			return bytes.Compare(operandA, operandB) > 0
		}
	case tpm2.OpUnsignedGT:
		return bytes.Compare(operandA, operandB) > 0
	case tpm2.OpSignedLT:
		switch {
		case len(operandA) == 0:
			return false
		case (operandA[0]^operandB[0])&0x80 > 0:
			return operandA[0]&0x80 > 0
		default:
			return bytes.Compare(operandA, operandB) < 0
		}
	case tpm2.OpUnsignedLT:
		return bytes.Compare(operandA, operandB) < 0
	case tpm2.OpSignedGE:
		switch {
		case len(operandA) == 0:
			return true
		case (operandA[0]^operandB[0])&0x80 > 0:
			return operandA[0]&0x80 == 0
		default:
			return bytes.Compare(operandA, operandB) >= 0
		}
	case tpm2.OpUnsignedGE:
		return bytes.Compare(operandA, operandB) >= 0
	case tpm2.OpSignedLE:
		switch {
		case len(operandA) == 0:
			return true
		case (operandA[0]^operandB[0])&0x80 > 0:
			return operandA[0]&0x80 > 0
		default:
			return bytes.Compare(operandA, operandB) <= 0
		}
	case tpm2.OpUnsignedLE:
		return bytes.Compare(operandA, operandB) <= 0
	case tpm2.OpBitset:
		for i := range operandA {
			if operandA[i]&operandB[i] != operandB[i] {
				return false
			}
		}
		return true
	case tpm2.OpBitclear:
		for i := range operandA {
			if operandA[i]&operandB[i] > 0 {
				return false
			}
		}
		return true
	default:
		panic("not reached")
	}
}

func (s *policyBranchSelector) canAuthNV(pub *tpm2.NVPublic, policy *Policy, command tpm2.CommandCode) bool {
	if pub.Attrs&tpm2.AttrNVPolicyRead == 0 {
		return false
	}
	if policy == nil {
		return false
	}

	details, err := policy.Details(pub.Name().Algorithm(), "")
	if err != nil {
		return false
	}

	for _, d := range details {
		if len(d.NV) > 0 {
			continue
		}
		if len(d.Secret) > 0 {
			continue
		}
		if len(d.Signed) > 0 {
			continue
		}
		if len(d.Authorize) > 0 {
			continue
		}
		if d.AuthValueNeeded {
			continue
		}
		code, set := d.CommandCode()
		if set && code != command {
			continue
		}
		if len(d.CounterTimer) > 0 {
			continue
		}
		if _, set := d.CpHash(); set {
			continue
		}
		if _, set := d.NameHash(); set {
			continue
		}
		if len(d.PCR) > 0 {
			continue
		}
		nvWritten, set := d.NvWritten()
		if set && !nvWritten {
			continue
		}
		return true
	}

	return false
}

func nvAssertionKey(nv *PolicyNVDetails) paramKey {
	h := crypto.SHA256.New()
	mu.MustMarshalToWriter(h, nv)

	var key paramKey
	copy(key[:], h.Sum(nil))
	return key
}

type nvIndexInfo struct {
	resource tpm2.ResourceContext
	pub      *tpm2.NVPublic
	policy   *Policy
}

func (s *policyBranchSelector) filterNVIncompatibleBranches(complete taskFn) error {
	nvData := make(map[paramKey][]byte)
	nvSeen := make(map[paramKey]struct{})
	nvInfo := make(map[tpm2.Handle]*nvIndexInfo)

	s.nvOk = make(map[paramKey]struct{})

	var tasks []taskFn
	for p, d := range s.detailsMap {
		incompatible := false
		for _, nv := range d.NV {
			nv := nv

			key := nvAssertionKey(&nv)
			if _, exists := nvSeen[key]; exists {
				continue
			}
			nvSeen[key] = struct{}{}

			info, exists := nvInfo[nv.Index]
			if !exists {
				resource, policy, err := s.resources.LoadName(nv.Name)
				if err != nil {
					// If this NV index doesn't exist with the specified name, then
					// this branch is never going to work
					incompatible = true
					break
				}
				pub, err := s.tpm.NVReadPublic(resource.Resource())
				if err != nil {
					return err
				}

				info = &nvIndexInfo{resource: resource.Resource(), pub: pub, policy: policy}
				nvInfo[nv.Index] = info
			}

			if !s.canAuthNV(info.pub, info.policy, tpm2.CommandNVRead) {
				continue
			}

			if int(nv.Offset) > int(info.pub.Size) {
				incompatible = true
				break
			}
			if int(nv.Offset)+len(nv.OperandB) > int(info.pub.Size) {
				incompatible = true
				break
			}

			// create a task to run the policy session and read the NV index
			task := func() error {
				session, err := s.tpm.StartAuthSession(tpm2.SessionTypePolicy, nv.Name.Algorithm())
				if err != nil {
					return err
				}

				params := &PolicyExecuteParams{
					Usage: NewPolicySessionUsage(tpm2.CommandNVRead, []Named{nv.Name, nv.Name}, uint16(len(nv.OperandB)), nv.Offset).NoAuthValue(),
				}

				runner := newPolicyRunner(
					newTpmPolicySession(s.tpm, session),
					new(nullTickets),
					new(nullPolicyResourceLoader),
					func(runner *policyRunner) policyRunnerHelper {
						return newExecutePolicyHelper(runner, s.tpm, params, s.subPolicyRunner, false)
					},
				)
				runner.pushElements(info.policy.policy.Policy)
				s.subPolicyRunner.pushRunner(
					runner,
					func(err error) error {
						defer s.tpm.FlushContext(session)
						if err != nil {
							// ignore policy execution error
							return nil
						}

						data, err := s.tpm.NVRead(info.resource, info.resource, uint16(len(nv.OperandB)), nv.Offset, session)
						if err != nil {
							// ignore NVRead error
							return nil
						}

						nvData[key] = data
						return nil
					},
				)
				return nil
			}
			tasks = append(tasks, task)
		}
		if incompatible {
			delete(s.detailsMap, p)
		}
	}

	s.controller.pushTasks(func() error {
		for p, d := range s.detailsMap {
			for _, nv := range d.NV {
				key := nvAssertionKey(&nv)
				data, exists := nvData[key]
				if !exists {
					// we can't check this assertion
					continue
				}

				operandA := data
				operandB := nv.OperandB

				if !s.bufferMatch(operandA, operandB, nv.Operation) {
					delete(s.detailsMap, p)
					break
				}

				info := nvInfo[nv.Index]
				if s.canAuthNV(info.pub, info.policy, tpm2.CommandPolicyNV) {
					s.nvOk[key] = struct{}{}
				}
			}
		}
		return complete()
	})
	s.controller.pushTasks(tasks...)

	return nil
}

func (s *policyBranchSelector) filterCounterTimerIncompatibleBranches() error {
	hasCounterTimerAssertions := false
	for _, d := range s.detailsMap {
		if len(d.CounterTimer) > 0 {
			hasCounterTimerAssertions = true
			break
		}
	}

	if !hasCounterTimerAssertions {
		return nil
	}

	timeInfo, err := s.tpm.ReadClock()
	if err != nil {
		return fmt.Errorf("cannot obtain time info: %w", err)
	}

	timeInfoData, err := mu.MarshalToBytes(timeInfo)
	if err != nil {
		return fmt.Errorf("cannot marshal time info: %w", err)
	}

	for p, d := range s.detailsMap {
		incompatible := false
		for _, item := range d.CounterTimer {
			if int(item.Offset) > len(timeInfoData) {
				incompatible = true
				break
			}
			if int(item.Offset)+len(item.OperandB) > len(timeInfoData) {
				incompatible = true
				break
			}

			operandA := timeInfoData[int(item.Offset) : int(item.Offset)+len(item.OperandB)]
			operandB := item.OperandB

			if !s.bufferMatch(operandA, operandB, item.Operation) {
				incompatible = true
				break
			}
		}

		if incompatible {
			delete(s.detailsMap, p)
		}
	}

	return nil
}

func (s *policyBranchSelector) selectPath(branches policyBranches, complete func(policyBranchPath) error) error {
	// reset state
	s.paths = nil
	s.detailsMap = make(map[policyBranchPath]PolicyBranchDetails)

	var (
		currentPath    policyBranchPath
		currentDetails PolicyBranchDetails
	)

	var walker *treeWalker
	walker = newTreeWalker(
		newProxyPolicySession(newNullPolicySession(s.sessionAlg), &currentDetails),
		s,
		func() (treeWalkerBeginBranchFn, treeWalkerEndBranchFn, error) {
			details := currentDetails
			path := currentPath

			return func(name policyBranchPath) error {
				currentPath = path.Concat(name)
				currentDetails = details
				walker.runner.setSession(newProxyPolicySession(newNullPolicySession(s.sessionAlg), &currentDetails))
				return nil
			}, nil, nil
		},
		func() error {
			s.detailsMap[currentPath] = currentDetails
			s.paths = append(s.paths, currentPath)
			return nil
		})
	if err := walker.run(policyElements{
		&policyElement{
			Type: tpm2.CommandPolicyOR,
			Details: &policyElementDetails{
				OR: &policyORElement{Branches: branches},
			},
		},
	}); err != nil {
		return fmt.Errorf("cannot perform tree walk: %w", err)
	}

	s.filterInvalidBranches()
	s.filterMissingResourceBranches()
	s.filterMissingAuthBranches()
	s.filterIgnoredResources()
	if err := s.filterUsageIncompatibleBranches(); err != nil {
		return fmt.Errorf("cannot filter branches incompatible with usage: %w", err)
	}
	if err := s.filterPcrIncompatibleBranches(); err != nil {
		return fmt.Errorf("cannot filter branches incompatible with TPM2_PolicyPCR assertions: %w", err)
	}
	if err := s.filterCounterTimerIncompatibleBranches(); err != nil {
		return fmt.Errorf("cannot filter branches incompatible with TPM2_PolicyCounterTimer assertions: %w", err)
	}
	if err := s.filterNVIncompatibleBranches(func() error {
		var candidates []policyBranchPath
		for _, path := range s.paths {
			if _, exists := s.detailsMap[path]; !exists {
				continue
			}
			candidates = append(candidates, path)
		}

		if len(candidates) == 0 {
			return errors.New("cannot select execution path: no appropriate paths found")
		}

		path := candidates[0]
		for _, candidate := range candidates {
			details := s.detailsMap[candidate]
			if details.AuthValueNeeded {
				continue
			}
			if len(details.Secret) > 0 {
				continue
			}
			if len(details.Signed) > 0 {
				continue
			}

			foundNV := false
			for _, nv := range details.NV {
				if _, ok := s.nvOk[nvAssertionKey(&nv)]; !ok {
					foundNV = true
					break
				}
			}
			if foundNV {
				continue
			}

			path = candidate
			break
		}

		return complete(path)
	}); err != nil {
		return fmt.Errorf("cannot filter branches incompatible with TPM2_PolicyNV assertions: %w", err)
	}

	return nil
}

func (s *policyBranchSelector) LoadAuthorizedPolicies(keySign tpm2.Name, policyRef tpm2.Nonce) ([]*Policy, error) {
	return s.resources.LoadAuthorizedPolicies(keySign, policyRef)
}

var errTreeWalkerSkipBranch = errors.New("")

type (
	treeWalkerBeginBranchNodeFn  func() (treeWalkerBeginBranchFn, treeWalkerEndBranchFn, error)
	treeWalkerBeginBranchFn      func(policyBranchPath) error
	treeWalkerEndBranchFn        func() error
	treeWalkerCompleteFullPathFn func() error
)

type treeWalkerHelper struct {
	sessionAlg tpm2.HashAlgorithmId
	controller policyRunnerController

	beginBranchNodeFn  treeWalkerBeginBranchNodeFn
	completeFullPathFn treeWalkerCompleteFullPathFn

	started          bool
	beginBranchQueue []taskFn
}

func newTreeWalkerHelper(runner *policyRunner, beginBranchNode treeWalkerBeginBranchNodeFn, completeFullPath treeWalkerCompleteFullPathFn) *treeWalkerHelper {
	return &treeWalkerHelper{
		sessionAlg:         runner.session().HashAlg(),
		controller:         runner,
		beginBranchNodeFn:  beginBranchNode,
		completeFullPathFn: completeFullPath,
	}
}

func (h *treeWalkerHelper) pushNextBranchWalk() {
	done := len(h.beginBranchQueue) == 0
	switch done {
	case false:
		task := h.beginBranchQueue[0]
		h.beginBranchQueue = h.beginBranchQueue[1:]
		h.controller.pushTasks(task)
	case true:
		h.started = false
	}
}

func (h *treeWalkerHelper) walkBranch(parentPath policyBranchPath, beginBranchFn treeWalkerBeginBranchFn, endBranchFn treeWalkerEndBranchFn, index int, branch *policyBranch, restoreTasks func()) error {
	if beginBranchFn != nil {
		name := policyBranchPath(branch.Name)
		if len(name) == 0 {
			name = policyBranchPath(fmt.Sprintf("$[%d]", index))
		}
		if err := beginBranchFn(name); err != nil {
			if err == errTreeWalkerSkipBranch {
				h.pushNextBranchWalk()
				return nil
			}
			return fmt.Errorf("cannot begin walk branch: %w", err)
		}
		h.controller.setCurrentPath(parentPath.Concat(name))
	}

	restoreTasks()
	if endBranchFn != nil {
		h.controller.pushTasks(func() error {
			if err := endBranchFn(); err != nil {
				return fmt.Errorf("cannot end walk branch: %w", err)
			}
			return nil
		})
	}
	h.controller.pushElements(branch.Policy)
	return nil
}

func (h *treeWalkerHelper) loadExternal(public *tpm2.Public) (ResourceContext, error) {
	// the handle is not relevant here
	resource := tpm2.NewLimitedResourceContext(0x80000000, public.Name())
	return newResourceContextFlushable(resource, nil), nil
}

func (h *treeWalkerHelper) cpHash(cpHash *policyCpHashElement) error {
	return nil
}

func (h *treeWalkerHelper) nameHash(nameHash *policyNameHashElement) error {
	return nil
}

func (h *treeWalkerHelper) authorize(auth tpm2.ResourceContext, policy *Policy, usage *PolicySessionUsage, prefer tpm2.SessionType, complete func(error, tpm2.SessionContext) error) error {
	h.controller.pushTasks(func() error {
		return complete(nil, nil)
	})
	return nil
}

func (h *treeWalkerHelper) handleBranches(branches policyBranches, complete func(tpm2.DigestList, int) error) error {
	if len(branches) == 0 {
		return errors.New("branch node with no branches")
	}

	restoreTasks, numOfTasks := h.controller.snapshotTasks()
	currentPath := h.controller.currentPath()

	if !h.started {
		if len(branches) != 1 || numOfTasks != 0 {
			return errors.New("internal error: inconsistent starting state")
		}
		if len(h.beginBranchQueue) != 0 {
			return errors.New("internal error: unexpected state")
		}

		h.controller.appendTask(func() error {
			if err := h.completeFullPathFn(); err != nil {
				return fmt.Errorf("cannot complete walk full path: %w", err)
			}
			h.pushNextBranchWalk()
			return nil
		})
		restoreTasks, _ = h.controller.snapshotTasks()
	}

	h.controller.clearTasks()

	var beginBranchFn treeWalkerBeginBranchFn
	var endBranchFn treeWalkerEndBranchFn
	if h.started {
		var err error
		beginBranchFn, endBranchFn, err = h.beginBranchNodeFn()
		if err != nil {
			return fmt.Errorf("cannot begin walk branch node: %w", err)
		}
	}

	var tasks []taskFn
	for i, branch := range branches {
		i := i
		branch := branch
		tasks = append(tasks, func() error {
			return h.walkBranch(currentPath, beginBranchFn, endBranchFn, i, branch, restoreTasks)
		})
	}

	h.beginBranchQueue = append(tasks, h.beginBranchQueue...)

	// run the first branch
	h.pushNextBranchWalk()

	h.started = true
	return nil
}

func (h *treeWalkerHelper) handleAuthorizedPolicy(keySign *tpm2.Public, policyRef tpm2.Nonce, policies []*Policy, complete func(tpm2.Digest, *tpm2.TkVerified) error) error {
	h.controller.pushTasks(func() error {
		if err := complete(nil, nil); err != nil {
			return fmt.Errorf("cannot complete: %w", err)
		}
		return nil
	})

	restoreTasks, _ := h.controller.snapshotTasks()
	h.controller.clearTasks()
	currentPath := h.controller.currentPath()

	beginBranchFn, endBranchFn, err := h.beginBranchNodeFn()
	if err != nil {
		return err
	}

	var tasks []taskFn
	for i, policy := range policies {
		i := i

		var branch *policyBranch
		for _, digest := range policy.policy.PolicyDigests {
			if digest.HashAlg != h.sessionAlg {
				continue
			}

			branch = &policyBranch{
				Name:   policyBranchName(fmt.Sprintf("%x", digest.Digest)),
				Policy: policy.policy.Policy,
			}
			break
		}
		if branch == nil {
			continue
		}

		tasks = append(tasks, func() error {
			return h.walkBranch(currentPath, beginBranchFn, endBranchFn, i, branch, restoreTasks)
		})
	}
	if len(tasks) == 0 {
		tasks = append(tasks, func() error {
			return h.walkBranch(currentPath, beginBranchFn, endBranchFn, 0, &policyBranch{Name: "…"}, restoreTasks)
		})
	}

	h.beginBranchQueue = append(tasks, h.beginBranchQueue...)

	// run the first branch
	h.pushNextBranchWalk()

	return nil
}

type treeWalker struct {
	runner *policyRunner
}

func newTreeWalker(session policySession, resources PolicyResourceLoader, beginBranchNode treeWalkerBeginBranchNodeFn, completeFullPath treeWalkerCompleteFullPathFn) *treeWalker {
	return &treeWalker{
		runner: newPolicyRunner(
			session,
			new(nullTickets),
			resources,
			func(runner *policyRunner) policyRunnerHelper {
				return newTreeWalkerHelper(runner, beginBranchNode, completeFullPath)
			},
		),
	}
}

func (w *treeWalker) run(elements policyElements) error {
	return w.runner.run(policyElements{
		&policyElement{
			Type: tpm2.CommandPolicyOR,
			Details: &policyElementDetails{
				OR: &policyORElement{Branches: policyBranches{{Policy: elements}}},
			},
		},
	})
}
