// Copyright 2023 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package policyutil

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/canonical/go-tpm2"
	"github.com/canonical/go-tpm2/mu"
)

const (
	commandPolicyBranchNode tpm2.CommandCode = 0x20010171
)

var (
	// ErrMissingDigest is returned from [Policy.Execute] when a TPM2_PolicyCpHash or
	// TPM2_PolicyNameHash assertion is missing a digest for the selected session algorithm.
	ErrMissingDigest = errors.New("missing digest for session algorithm")
)

type paramKey [sha256.Size]byte

func policyParamKey(authName tpm2.Name, policyRef tpm2.Nonce) paramKey {
	h := crypto.SHA256.New()
	h.Write(authName)
	h.Write(policyRef)

	var key paramKey
	copy(key[:], h.Sum(nil))
	return key
}

// PolicyTicket corresponds to a ticket generated from a TPM2_PolicySigned or TPM2_PolicySecret
// assertion and is returned by [Policy.Execute]. Generated tickets can be supplied to
// [Policy.Execute] in the future in order to satisfy these assertions as long as they haven't
// expired.
type PolicyTicket struct {
	AuthName  tpm2.Name    // The name of the auth object associated with the corresponding assertion
	PolicyRef tpm2.Nonce   // The policy ref of the corresponding assertion
	CpHash    tpm2.Digest  // The cpHash supplied to the assertion that generated this ticket
	Timeout   tpm2.Timeout // The timeout returned by the assertion that generated this ticket

	// Ticket is the actual ticket returned by the TPM for the assertion that generated this ticket.
	// The Tag field indicates whether this was generated by TPM2_PolicySigned or TPM2_PolicySecret.
	Ticket *tpm2.TkAuth
}

// PolicySecretParams provides a way for an application to customize the cpHash and expiration
// arguments of a TPM2_PolicySecret assertion with the specified reference and for a resource
// with the specified name. These parameters aren't part of the policy because they aren't
// cryptographically bound to the policy digest and can be modified.
type PolicySecretParams struct {
	AuthName  tpm2.Name  // The name of the auth object associated with the corresponding TPM2_PolicySecret assertion
	PolicyRef tpm2.Nonce // The policy ref of the corresponding assertion
	CpHash    CpHash     // The command parameters to restrict the session usage to

	// Expiration specifies a timeout based on the absolute value of this field in seconds, after
	// which the authorization will expire. The timeout is measured from the time that the most
	// recent TPM nonce was generated for the session. This can be used to request a ticket that
	// can be used in a subsequent policy execution by specifying a negative value, in which case
	// this field and the CpHash field restrict the validity period and scope of the returned
	// ticket.
	Expiration int32
}

// AuthorizationError is returned from [Policy.Execute] if the policy uses TPM2_PolicySecret
// and the associated object could not be authorized, or if the policy uses TPM2_PolicySigned
// and no or an invalid signed authorization was supplied.
type AuthorizationError struct {
	AuthName  tpm2.Name
	PolicyRef tpm2.Nonce
	err       error
}

func (e *AuthorizationError) Error() string {
	return fmt.Sprintf("authorization failed for assertion with authName=%#x, policyRef=%#x: %v", e.AuthName, e.PolicyRef, e.err)
}

func (e *AuthorizationError) Unwrap() error {
	return e.err
}

// ResourceLoadError is returned from [Policy.Execute] if the policy required a resource that
// could not be loaded.
type ResourceLoadError struct {
	Name tpm2.Name
	err  error
}

func (e *ResourceLoadError) Error() string {
	return fmt.Sprintf("cannot load resource with name %#x: %v", e.Name, e.err)
}

func (e *ResourceLoadError) Unwrap() error {
	return e.err
}

// PolicyBranchName corresponds to the name of a branch. Valid names are UTF-8
// strings that start with characters other than '$'. A branch doesn't have to have
// a name, in which case it can be selected by its index.
type PolicyBranchName string

func (n PolicyBranchName) isValid() bool {
	if !utf8.ValidString(string(n)) {
		return false
	}
	if len(n) > 0 && n[0] == '$' {
		return false
	}
	return true
}

func (n PolicyBranchName) Marshal(w io.Writer) error {
	if !n.isValid() {
		return errors.New("invalid name")
	}
	_, err := mu.MarshalToWriter(w, []byte(n))
	return err
}

func (n *PolicyBranchName) Unmarshal(r io.Reader) error {
	var b []byte
	if _, err := mu.UnmarshalFromReader(r, &b); err != nil {
		return err
	}
	name := PolicyBranchName(b)
	if !name.isValid() {
		return errors.New("invalid name")
	}
	*n = name
	return nil
}

// PolicyBranchPath uniquely identifies an execution path through the branches in a
// profile, with each branch selector component being separated by a '/' character. A
// branch selector selects a branch at a node, and a branch can either be selected by
// its name (if it has one), or a numeric identifier of the form "$[n]" which selects
// a branch at a node using its index.
//
// The component "$auto" enables autoselection for a node, where a branch will be selected
// automatically. This only works for branches containing TPM2_PolicyPCR assertions
// where the assertion parameters match the current PCR values.
type PolicyBranchPath string

func (p PolicyBranchPath) popNextComponent() (next PolicyBranchPath, remaining PolicyBranchPath) {
	remaining = p
	for len(remaining) > 0 {
		s := strings.SplitN(string(remaining), "/", 2)
		remaining = ""
		if len(s) == 2 {
			remaining = PolicyBranchPath(s[1])
		}
		component := PolicyBranchPath(s[0])
		if len(component) > 0 {
			return component, remaining
		}
	}

	return "", ""
}

// PolicySessionUsage describes how a policy session will be used, and assists with
// automatically selecting branches where a policy has command context-specific branches.
type PolicySessionUsage struct {
	commandCode     tpm2.CommandCode
	handles         []Named
	params          []interface{}
	nvHandle        tpm2.Handle
	canUseAuthValue bool
}

// NewPolicySessionUsage creates a new PolicySessionUsage.
func NewPolicySessionUsage(command tpm2.CommandCode, handles []Named, params ...interface{}) *PolicySessionUsage {
	return &PolicySessionUsage{
		commandCode: command,
		handles:     handles,
		params:      params,
	}
}

// CanUseAuthValue indicates that the auth value for the resource being authorized
// can be provided when the policy session is used.
func (u *PolicySessionUsage) CanUseAuthValue() *PolicySessionUsage {
	u.canUseAuthValue = true
	return u
}

// WithNVHandle indicates that the policy session is being used to authorize a NV
// index with the specified handle. This will panic if handle is not a NV index.
func (u *PolicySessionUsage) WithNVHandle(handle tpm2.Handle) *PolicySessionUsage {
	if handle.Type() != tpm2.HandleTypeNVIndex {
		panic("invalid handle")
	}
	u.nvHandle = handle
	return u
}

// PolicyExecuteParams contains parameters that are useful for executing a policy.
type PolicyExecuteParams struct {
	SecretParams         []*PolicySecretParams        // Parameters for TPM2_PolicySecret assertions
	SignedAuthorizations []*PolicySignedAuthorization // Authorizations for TPM2_PolicySigned assertions
	Tickets              []*PolicyTicket              // Tickets for TPM2_PolicySecret and TPM2_PolicySigned assertions

	// Usage describes how the executed policy will be used, and assists with
	// automatically selecting branches where a policy has command context-specific
	// branches.
	Usage *PolicySessionUsage

	// Path provides a way to explicitly select branches to execute.
	Path PolicyBranchPath
}

type policyParams interface {
	secretParams(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySecretParams
	signedAuthorization(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySignedAuthorization
	ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket
}

type policyDeferredTaskElement struct {
	taskName string
	fn       func() error
}

func newDeferredTask(name string, fn func() error) *policyDeferredTaskElement {
	return &policyDeferredTaskElement{
		taskName: name,
		fn:       fn,
	}
}

func (e *policyDeferredTaskElement) name() string {
	return e.taskName
}

func (e *policyDeferredTaskElement) run(context policySessionContext) error {
	return e.fn()
}

type policyFlowHandler interface {
	handleBranches(branches policyBranches) error
	pushComputeContext(digest *taggedHash) func()
}

type policySessionContext interface {
	session() Session
	params() policyParams
	resources() ResourceLoader
	flowHandler() policyFlowHandler

	ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket
	addTicket(ticket *PolicyTicket)
}

type policyRunDispatcher interface {
	runBatchNext(tasks []policySessionTask)
	runNext(name string, fn func() error)
	runElementsNext(elements policyElements, done func() error)
}

// executePolicyParams is an implementation of policyParams that provides real
// parameters.
type executePolicyParams struct {
	policySecretParams map[paramKey]*PolicySecretParams
	authorizations     map[paramKey]*PolicySignedAuthorization
	tickets            map[paramKey]*PolicyTicket
}

func newExecutePolicyParams(params *PolicyExecuteParams) *executePolicyParams {
	out := &executePolicyParams{
		policySecretParams: make(map[paramKey]*PolicySecretParams),
		authorizations:     make(map[paramKey]*PolicySignedAuthorization),
		tickets:            make(map[paramKey]*PolicyTicket),
	}
	for _, param := range params.SecretParams {
		out.policySecretParams[policyParamKey(param.AuthName, param.PolicyRef)] = param
	}
	for _, auth := range params.SignedAuthorizations {
		if auth.Authorization == nil {
			continue
		}
		out.authorizations[policyParamKey(auth.Authorization.AuthKey.Name(), auth.Authorization.PolicyRef)] = auth
	}
	for _, ticket := range params.Tickets {
		out.tickets[policyParamKey(ticket.AuthName, ticket.PolicyRef)] = ticket
	}

	return out
}

func (p *executePolicyParams) secretParams(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySecretParams {
	return p.policySecretParams[policyParamKey(authName, policyRef)]
}

func (p *executePolicyParams) signedAuthorization(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySignedAuthorization {
	return p.authorizations[policyParamKey(authName, policyRef)]
}

func (p *executePolicyParams) ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket {
	return p.tickets[policyParamKey(authName, policyRef)]
}

type executePolicyFlowHandler struct {
	state     TPMState
	runner    *policyRunner
	remaining PolicyBranchPath
	usage     *PolicySessionUsage
}

func newExecutePolicyFlowHandler(state TPMState, runner *policyRunner, params *PolicyExecuteParams) *executePolicyFlowHandler {
	return &executePolicyFlowHandler{
		state:     state,
		runner:    runner,
		remaining: params.Path,
		usage:     params.Usage,
	}
}

func (h *executePolicyFlowHandler) selectAndRunNextBranch(branches policyBranches, next PolicyBranchName) error {
	var selected int
	switch {
	case next[0] == '$':
		// select branch by index
		if _, err := fmt.Sscanf(string(next), "$[%d]", &selected); err != nil {
			return fmt.Errorf("cannot select branch: badly formatted path component \"%s\": %w", next, err)
		}
		if selected < 0 || selected >= len(branches) {
			return fmt.Errorf("cannot select branch: selected path %d out of range", selected)
		}
	default:
		// select branch by name
		selected = -1
		for i, branch := range branches {
			if len(branch.Name) == 0 {
				continue
			}
			if branch.Name == next {
				selected = i
				break
			}
		}
		if selected == -1 {
			return fmt.Errorf("cannot select branch: no branch with name \"%s\"", next)
		}
	}

	if selected == -1 {
		// the switch branches should have returned a specific error already
		panic("not reached")
	}

	context := &policyBranchNodeContext{
		dispatcher:  h.runner,
		session:     h.runner.session(),
		flowHandler: h.runner.flowHandler(),
		branches:    branches,
		selected:    selected,
	}

	return context.collectBranchDigests(func() error {
		return context.runSelectedBranch(func() error {
			return context.completeBranchNode()
		})
	})
}

func (h *executePolicyFlowHandler) handleBranches(branches policyBranches) error {
	next, remaining := h.remaining.popNextComponent()
	if len(next) > 0 {
		h.remaining = remaining
		return h.selectAndRunNextBranch(branches, PolicyBranchName(next))
	}

	autoSelector := newPolicyBranchAutoSelector(h.state, h.runner, h.usage)
	return autoSelector.selectBranch(branches, func(path PolicyBranchPath) error {
		h.remaining = path
		return h.handleBranches(branches)
	})
}

func (h *executePolicyFlowHandler) pushComputeContext(digest *taggedHash) (restore func()) {
	oldContext := h.runner.policyRunnerContext
	h.runner.policyRunnerContext = newComputePolicyRunnerContext(h.runner, digest)

	return func() {
		h.runner.policyRunnerContext = oldContext
	}
}

type policySessionTask interface {
	name() string
	run(context policySessionContext) error
}

type taggedHash struct {
	HashAlg tpm2.HashAlgorithmId
	Digest  tpm2.Digest
}

func (h taggedHash) Marshal(w io.Writer) error {
	ta := tpm2.MakeTaggedHash(h.HashAlg, h.Digest)
	_, err := mu.MarshalToWriter(w, ta)
	return err
}

func (h *taggedHash) Unmarshal(r io.Reader) error {
	var ta tpm2.TaggedHash
	if _, err := mu.UnmarshalFromReader(r, &ta); err != nil {
		return err
	}

	if ta.HashAlg != tpm2.HashAlgorithmNull && !ta.HashAlg.IsValid() {
		return errors.New("invalid digest algorithm")
	}

	*h = taggedHash{
		HashAlg: ta.HashAlg,
		Digest:  ta.Digest()}
	return nil
}

type taggedHashList []taggedHash

type policyNV struct {
	NvIndex   *tpm2.NVPublic
	OperandB  tpm2.Operand
	Offset    uint16
	Operation tpm2.ArithmeticOp
}

func (*policyNV) name() string { return "TPM2_PolicyNV assertion" }

func (e *policyNV) run(context policySessionContext) error {
	nvIndex, err := tpm2.NewNVIndexResourceContextFromPub(e.NvIndex)
	if err != nil {
		return fmt.Errorf("cannot create nvIndex context: %w", err)
	}

	auth := nvIndex
	switch {
	default:
	case e.NvIndex.Attrs&tpm2.AttrNVOwnerRead != 0:
		auth, err = context.resources().LoadHandle(tpm2.HandleOwner)
	case e.NvIndex.Attrs&tpm2.AttrNVPPRead != 0:
		auth, err = context.resources().LoadHandle(tpm2.HandlePlatform)
	}
	if err != nil {
		return fmt.Errorf("cannot create auth context: %w", err)
	}

	session, _, err := context.resources().NeedAuthorize(auth)
	if err != nil {
		return fmt.Errorf("cannot authorize auth object: %w", err)
	}
	defer func() {
		if session == nil {
			return
		}
		session.Close()
	}()

	var tpmSession tpm2.SessionContext
	if session != nil {
		tpmSession = session.Session()
	}

	return context.session().PolicyNV(auth, nvIndex, e.OperandB, e.Offset, e.Operation, tpmSession)
}

type policySecret struct {
	AuthObjectName tpm2.Name
	PolicyRef      tpm2.Nonce
}

func (*policySecret) name() string { return "TPM2_PolicySecret assertion" }

func (e *policySecret) run(context policySessionContext) error {
	if ticket := context.ticket(e.AuthObjectName, e.PolicyRef); ticket != nil {
		err := context.session().PolicyTicket(ticket.Timeout, ticket.CpHash, ticket.PolicyRef, ticket.AuthName, ticket.Ticket)
		switch {
		case tpm2.IsTPMParameterError(err, tpm2.ErrorExpired, tpm2.CommandPolicyTicket, 1):
			// The ticket has expired - ignore this and fall through to PolicySecret
		case tpm2.IsTPMParameterError(err, tpm2.ErrorTicket, tpm2.CommandPolicyTicket, 5):
			// The ticket is invalid - ignore this and fall through to PolicySecret
		case err != nil:
			return &AuthorizationError{AuthName: e.AuthObjectName, PolicyRef: e.PolicyRef, err: err}
		default:
			// The ticket was accepted
			return nil
		}
	}

	params := context.params().secretParams(e.AuthObjectName, e.PolicyRef)
	if params == nil {
		var nilParams PolicySecretParams
		params = &nilParams
	}

	var cpHashA tpm2.Digest
	if params.CpHash != nil {
		var err error
		cpHashA, err = params.CpHash.Digest(context.session().HashAlg())
		if err != nil {
			return fmt.Errorf("cannot obtain cpHashA: %w", err)
		}
	}

	authObject, err := context.resources().LoadName(e.AuthObjectName)
	if err != nil {
		return &ResourceLoadError{Name: e.AuthObjectName, err: err}
	}
	defer authObject.Flush()

	session, _, err := context.resources().NeedAuthorize(authObject.Resource())
	if err != nil {
		return &AuthorizationError{AuthName: e.AuthObjectName, PolicyRef: e.PolicyRef, err: err}
	}
	defer func() {
		if session == nil {
			return
		}
		session.Close()
	}()

	var tpmSession tpm2.SessionContext
	if session != nil {
		tpmSession = session.Session()
	}

	timeout, ticket, err := context.session().PolicySecret(authObject.Resource(), cpHashA, e.PolicyRef, params.Expiration, tpmSession)
	if err != nil {
		return &AuthorizationError{AuthName: e.AuthObjectName, PolicyRef: e.PolicyRef, err: err}
	}

	context.addTicket(&PolicyTicket{
		AuthName:  e.AuthObjectName,
		PolicyRef: e.PolicyRef,
		CpHash:    cpHashA,
		Timeout:   timeout,
		Ticket:    ticket})
	return nil
}

type policySigned struct {
	AuthKeyName tpm2.Name
	PolicyRef   tpm2.Nonce
}

func (*policySigned) name() string { return "TPM2_PolicySigned assertion" }

func (e *policySigned) run(context policySessionContext) error {
	if ticket := context.ticket(e.AuthKeyName, e.PolicyRef); ticket != nil {
		err := context.session().PolicyTicket(ticket.Timeout, ticket.CpHash, ticket.PolicyRef, ticket.AuthName, ticket.Ticket)
		switch {
		case tpm2.IsTPMParameterError(err, tpm2.ErrorExpired, tpm2.CommandPolicyTicket, 1):
			// The ticket has expired - ignore this and fall through to PolicySigned
		case tpm2.IsTPMParameterError(err, tpm2.ErrorTicket, tpm2.CommandPolicyTicket, 5):
			// The ticket is invalid - ignore this and fall through to PolicySigned
		case err != nil:
			return &AuthorizationError{AuthName: e.AuthKeyName, PolicyRef: e.PolicyRef, err: err}
		default:
			// The ticket was accepted
			return nil
		}
	}

	auth := context.params().signedAuthorization(e.AuthKeyName, e.PolicyRef)
	if auth == nil {
		return &AuthorizationError{
			AuthName:  e.AuthKeyName,
			PolicyRef: e.PolicyRef,
			err:       errors.New("missing signed authorization"),
		}
	}

	authKey, err := context.resources().LoadExternal(auth.Authorization.AuthKey)
	if err != nil {
		return fmt.Errorf("cannot create authKey context: %w", err)
	}
	defer authKey.Flush()

	includeNonceTPM := false
	if len(auth.NonceTPM) > 0 {
		includeNonceTPM = true
	}

	timeout, ticket, err := context.session().PolicySigned(authKey.Resource(), includeNonceTPM, auth.CpHash, e.PolicyRef, auth.Expiration, auth.Authorization.Signature)
	if err != nil {
		return &AuthorizationError{AuthName: e.AuthKeyName, PolicyRef: e.PolicyRef, err: err}
	}

	context.addTicket(&PolicyTicket{
		AuthName:  authKey.Resource().Name(),
		PolicyRef: e.PolicyRef,
		CpHash:    auth.CpHash,
		Timeout:   timeout,
		Ticket:    ticket})
	return nil
}

type policyAuthValue struct{}

func (*policyAuthValue) name() string { return "TPM2_PolicyAuthValue assertion" }

func (*policyAuthValue) run(context policySessionContext) error {
	return context.session().PolicyAuthValue()
}

type policyCommandCode struct {
	CommandCode tpm2.CommandCode
}

func (*policyCommandCode) name() string { return "TPM2_PolicyCommandCode assertion" }

func (e *policyCommandCode) run(context policySessionContext) error {
	return context.session().PolicyCommandCode(e.CommandCode)
}

type policyCounterTimer struct {
	OperandB  tpm2.Operand
	Offset    uint16
	Operation tpm2.ArithmeticOp
}

func (*policyCounterTimer) name() string { return "TPM2_PolicyCounterTimer assertion" }

func (e *policyCounterTimer) run(context policySessionContext) error {
	return context.session().PolicyCounterTimer(e.OperandB, e.Offset, e.Operation)
}

type policyCpHash struct {
	Digests taggedHashList
}

func (*policyCpHash) name() string { return "TPM2_PolicyCpHash assertion" }

func (e *policyCpHash) run(context policySessionContext) error {
	var cpHashA tpm2.Digest
	for _, digest := range e.Digests {
		if digest.HashAlg != context.session().HashAlg() {
			continue
		}
		cpHashA = digest.Digest
		break
	}
	if cpHashA == nil {
		return ErrMissingDigest
	}
	return context.session().PolicyCpHash(cpHashA)
}

type policyNameHash struct {
	Digests taggedHashList
}

func (*policyNameHash) name() string { return "TPM2_PolicyNameHash assertion" }

func (e *policyNameHash) run(context policySessionContext) error {
	var nameHash tpm2.Digest
	for _, digest := range e.Digests {
		if digest.HashAlg != context.session().HashAlg() {
			continue
		}
		nameHash = digest.Digest
		break
	}
	if nameHash == nil {
		return ErrMissingDigest
	}
	return context.session().PolicyNameHash(nameHash)
}

type policyBranch struct {
	Name          PolicyBranchName
	PolicyDigests taggedHashList
	Policy        policyElements
}

type policyBranches []policyBranch

type policyBranchNode struct {
	Branches policyBranches
}

func (*policyBranchNode) name() string { return "branch node" }

func (e *policyBranchNode) run(context policySessionContext) error {
	return context.flowHandler().handleBranches(e.Branches)
}

type policyBranchNodeContext struct {
	dispatcher    policyRunDispatcher
	session       Session
	flowHandler   policyFlowHandler
	branches      policyBranches
	currentDigest tpm2.Digest
	digests       tpm2.DigestList
	selected      int
}

func (c *policyBranchNodeContext) ensureCurrentDigest() error {
	if len(c.currentDigest) == c.session.HashAlg().Size() {
		return nil
	}

	currentDigest, err := c.session.PolicyGetDigest()
	if err != nil {
		return err
	}
	c.currentDigest = currentDigest
	return nil
}

func (c *policyBranchNodeContext) commitBranchDigest(index int, digest tpm2.Digest, done func() error) error {
	if index != len(c.digests) {
		return errors.New("internal error: unexpected digest")
	}
	c.digests = append(c.digests, digest)

	if len(c.digests) != len(c.branches) {
		return nil
	}

	return done()
}

func (c *policyBranchNodeContext) collectBranchDigests(done func() error) error {
	// queue elements to obtain the digest for each branch. This is done asynchronously
	// because they may have to descend in to each branch to compute the digest, although
	// this is only the case during policy execution.
	var tasks []policySessionTask
	for i := range c.branches {
		i := i
		task := newDeferredTask("collect branch digest", func() error {
			return c.collectBranchDigest(i, func(digest tpm2.Digest) error {
				return c.commitBranchDigest(i, digest, done)
			})
		})
		tasks = append(tasks, task)
	}
	c.dispatcher.runBatchNext(tasks)

	return nil
}

func (c *policyBranchNodeContext) computeBranchDigests(done func() error) error {
	var tasks []policySessionTask
	for i := range c.branches {
		i := i
		task := newDeferredTask("compute branch digest", func() error {
			return c.computeBranchDigest(i, func(digest tpm2.Digest) error {
				return c.commitBranchDigest(i, digest, done)
			})
		})
		tasks = append(tasks, task)
	}
	c.dispatcher.runBatchNext(tasks)

	return nil
}

func (c *policyBranchNodeContext) collectBranchDigest(index int, done func(tpm2.Digest) error) error {
	// see if the branch has a stored value for the current algorithm
	for _, digest := range c.branches[index].PolicyDigests {
		if digest.HashAlg != c.session.HashAlg() {
			continue
		}

		// we have a digest
		return done(digest.Digest)
	}

	c.dispatcher.runNext("compute branch digest", func() error {
		return c.computeBranchDigest(index, done)
	})
	return nil
}

func (c *policyBranchNodeContext) computeBranchDigest(index int, done func(tpm2.Digest) error) error {
	// we need to compute the digest, so ensure we have the current session
	// digest.
	if err := c.ensureCurrentDigest(); err != nil {
		return err
	}

	// push a new run context that will consume the policy assertions for this
	// branch so that we can compute its digest.
	digest := &taggedHash{
		HashAlg: c.session.HashAlg(),
		Digest:  make(tpm2.Digest, c.session.HashAlg().Size()),
	}
	copy(digest.Digest, c.currentDigest)
	restore := c.flowHandler.pushComputeContext(digest)

	c.dispatcher.runElementsNext(c.branches[index].Policy, func() error {
		restore()
		return done(digest.Digest)
	})

	return nil
}

func (c *policyBranchNodeContext) runSelectedBranch(done func() error) error {
	c.dispatcher.runElementsNext(c.branches[c.selected].Policy, done)
	return nil
}

func (c *policyBranchNodeContext) completeBranchNode() error {
	tree, err := newPolicyOrTree(c.session.HashAlg(), c.digests)
	if err != nil {
		return fmt.Errorf("cannot compute PolicyOR tree: %w", err)
	}
	c.dispatcher.runBatchNext(tree.selectBranch(c.selected))
	return nil
}

type policyOR struct {
	HashList []taggedHashList
}

func (*policyOR) name() string { return "TPM2_PolicyOR assertion" }

func (e *policyOR) run(context policySessionContext) error {
	var pHashList tpm2.DigestList
	for i, h := range e.HashList {
		found := false
		for _, digest := range h {
			if digest.HashAlg != context.session().HashAlg() {
				continue
			}
			pHashList = append(pHashList, digest.Digest)
			found = true
			break
		}
		if !found {
			return fmt.Errorf("cannot process digest at index %d: %w", i, ErrMissingDigest)
		}
	}

	return context.session().PolicyOR(pHashList)
}

type pcrValue struct {
	PCR    tpm2.Handle
	Digest taggedHash
}

type pcrValueList []pcrValue

type policyPCR struct {
	PCRs pcrValueList
}

func (*policyPCR) name() string { return "TPM2_PolicyPCR assertion" }

func (e *policyPCR) run(context policySessionContext) error {
	values, err := e.pcrValues()
	if err != nil {
		return err
	}
	pcrs, pcrDigest, err := ComputePCRDigestFromAllValues(context.session().HashAlg(), values)
	if err != nil {
		return fmt.Errorf("cannot compute PCR digest: %w", err)
	}
	return context.session().PolicyPCR(pcrDigest, pcrs)
}

func (e *policyPCR) pcrValues() (tpm2.PCRValues, error) {
	values := make(tpm2.PCRValues)
	for i, value := range e.PCRs {
		if value.PCR.Type() != tpm2.HandleTypePCR {
			return nil, fmt.Errorf("invalid PCR handle at index %d", i)
		}
		if err := values.SetValue(value.Digest.HashAlg, int(value.PCR), value.Digest.Digest); err != nil {
			return nil, fmt.Errorf("invalid PCR value at index %d: %w", i, err)
		}
	}
	return values, nil
}

type policyDuplicationSelect struct {
	Object        tpm2.Name
	NewParent     tpm2.Name
	IncludeObject bool
}

func (*policyDuplicationSelect) name() string { return "TPM2_PolicyDuplicationSelect assertion" }

func (e *policyDuplicationSelect) run(context policySessionContext) error {
	return context.session().PolicyDuplicationSelect(e.Object, e.NewParent, e.IncludeObject)
}

type policyPassword struct{}

func (*policyPassword) name() string { return "TPM2_PolicyPassword assertion" }

func (*policyPassword) run(context policySessionContext) error {
	return context.session().PolicyPassword()
}

type policyNvWritten struct {
	WrittenSet bool
}

func (*policyNvWritten) name() string { return "TPM2_PolicyNvWritten assertion" }

func (e *policyNvWritten) run(context policySessionContext) error {
	return context.session().PolicyNvWritten(e.WrittenSet)
}

type policyElementDetails struct {
	NV                *policyNV
	Secret            *policySecret
	Signed            *policySigned
	AuthValue         *policyAuthValue
	CommandCode       *policyCommandCode
	CounterTimer      *policyCounterTimer
	CpHash            *policyCpHash
	NameHash          *policyNameHash
	OR                *policyOR
	PCR               *policyPCR
	DuplicationSelect *policyDuplicationSelect
	Password          *policyPassword
	NvWritten         *policyNvWritten

	BranchNode *policyBranchNode
}

func (d *policyElementDetails) Select(selector reflect.Value) interface{} {
	switch selector.Interface().(tpm2.CommandCode) {
	case tpm2.CommandPolicyNV:
		return &d.NV
	case tpm2.CommandPolicySecret:
		return &d.Secret
	case tpm2.CommandPolicySigned:
		return &d.Signed
	case tpm2.CommandPolicyAuthValue:
		return &d.AuthValue
	case tpm2.CommandPolicyCommandCode:
		return &d.CommandCode
	case tpm2.CommandPolicyCounterTimer:
		return &d.CounterTimer
	case tpm2.CommandPolicyCpHash:
		return &d.CpHash
	case tpm2.CommandPolicyNameHash:
		return &d.NameHash
	case tpm2.CommandPolicyOR:
		return &d.OR
	case tpm2.CommandPolicyPCR:
		return &d.PCR
	case tpm2.CommandPolicyDuplicationSelect:
		return &d.DuplicationSelect
	case tpm2.CommandPolicyPassword:
		return &d.Password
	case tpm2.CommandPolicyNvWritten:
		return &d.NvWritten
	case commandPolicyBranchNode:
		return &d.BranchNode
	default:
		return nil
	}
}

type policyElement struct {
	Type    tpm2.CommandCode
	Details *policyElementDetails
}

func (e *policyElement) runner() policySessionTask {
	switch e.Type {
	case tpm2.CommandPolicyNV:
		return e.Details.NV
	case tpm2.CommandPolicySecret:
		return e.Details.Secret
	case tpm2.CommandPolicySigned:
		return e.Details.Signed
	case tpm2.CommandPolicyAuthValue:
		return e.Details.AuthValue
	case tpm2.CommandPolicyCommandCode:
		return e.Details.CommandCode
	case tpm2.CommandPolicyCounterTimer:
		return e.Details.CounterTimer
	case tpm2.CommandPolicyCpHash:
		return e.Details.CpHash
	case tpm2.CommandPolicyNameHash:
		return e.Details.NameHash
	case tpm2.CommandPolicyOR:
		return e.Details.OR
	case tpm2.CommandPolicyPCR:
		return e.Details.PCR
	case tpm2.CommandPolicyDuplicationSelect:
		return e.Details.DuplicationSelect
	case tpm2.CommandPolicyPassword:
		return e.Details.Password
	case tpm2.CommandPolicyNvWritten:
		return e.Details.NvWritten
	case commandPolicyBranchNode:
		return e.Details.BranchNode
	default:
		panic("invalid type")
	}
}

func (e *policyElement) name() string {
	return e.runner().name()
}

func (e *policyElement) run(context policySessionContext) error {
	return e.runner().run(context)
}

type policyElements []*policyElement

type policy struct {
	Policy policyElements
}

// Policy corresponds to an authorization policy. It can be serialized with
// [github.com/canonical/go-tpm2/mu].
type Policy struct {
	policy policy
}

// Marshal implements [mu.CustomMarshaller.Marshal].
func (p Policy) Marshal(w io.Writer) error {
	_, err := mu.MarshalToWriter(w, p.policy)
	return err
}

// Unmarshal implements [mu.CustomMarshaller.Unarshal].
func (p *Policy) Unmarshal(r io.Reader) error {
	_, err := mu.UnmarshalFromReader(r, &p.policy)
	return err
}

type policyRunnerContext struct {
	policySession     Session
	policyParams      policyParams
	policyResources   ResourceLoader
	policyFlowHandler policyFlowHandler

	tickets map[paramKey]*PolicyTicket
}

func newPolicyRunnerContext(session Session, params policyParams, resources ResourceLoader, flowHandler policyFlowHandler) *policyRunnerContext {
	return &policyRunnerContext{
		policySession:     session,
		policyParams:      params,
		policyResources:   resources,
		policyFlowHandler: flowHandler,
		tickets:           make(map[paramKey]*PolicyTicket),
	}
}

type policyRunner struct {
	*policyRunnerContext
	tasks []policySessionTask
	next  []policySessionTask
}

func (r *policyRunner) session() Session {
	return r.policySession
}

func (r *policyRunner) params() policyParams {
	return r.policyParams
}

func (r *policyRunner) resources() ResourceLoader {
	return r.policyResources
}

func (r *policyRunner) flowHandler() policyFlowHandler {
	return r.policyFlowHandler
}

func (r *policyRunner) ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket {
	if ticket, exists := r.tickets[policyParamKey(authName, policyRef)]; exists {
		return ticket
	}
	return r.policyParams.ticket(authName, policyRef)
}

func (r *policyRunner) addTicket(ticket *PolicyTicket) {
	if ticket.Ticket == nil || (ticket.Ticket.Hierarchy == tpm2.HandleNull && len(ticket.Ticket.Digest) == 0) {
		// skip null tickets
		return
	}
	r.tickets[policyParamKey(ticket.AuthName, ticket.PolicyRef)] = ticket
}

func (r *policyRunner) runBatchNext(tasks []policySessionTask) {
	r.next = append(tasks, r.next...)
}

func (r *policyRunner) runNext(name string, fn func() error) {
	r.next = append([]policySessionTask{newDeferredTask(name, fn)}, r.next...)
}

func (r *policyRunner) runElementsNext(elements policyElements, done func() error) {
	var tasks []policySessionTask
	for _, element := range elements {
		tasks = append(tasks, element)
	}
	if done != nil {
		tasks = append(tasks, newDeferredTask("callback", done))
	}
	r.runBatchNext(tasks)
}

func (r *policyRunner) commitNext() {
	if len(r.next) == 0 {
		return
	}
	r.tasks = append(r.next, r.tasks...)
	r.next = nil
}

func (r *policyRunner) more() bool {
	return len(r.next) > 0 || len(r.tasks) > 0
}

func (r *policyRunner) popTask() policySessionTask {
	r.commitNext()
	task := r.tasks[0]
	r.tasks = r.tasks[1:]
	return task
}

func (r *policyRunner) run(policy policyElements) error {
	r.runElementsNext(policy, nil)

	for r.more() {
		task := r.popTask()
		if err := task.run(r); err != nil {
			return fmt.Errorf("cannot process %s: %w", task.name(), err)
		}
	}

	return nil
}

// Execute runs this policy using the supplied TPM context and on the supplied policy session.
//
// The caller may supply additional parameters via the PolicyExecuteParams struct, which is an
// optional argument. This can contain parameters for TPM2_PolicySecret assertions, signed
// authorizations for TPM2_PolicySigned assertions, or tickets to satisfy TPM2_PolicySecret or
// TPM2_PolicySigned assertions. Each of these parameters are associated with a policy assertion
// by a name and policy reference.
//
// Resources required by a policy are obtained from the supplied ResourceLoader, which is optional
// but must be supplied for any policy that executes TPM2_PolicyNV, TPM2_PolicySecret or
// TPM2_PolicySigned assertions.
//
// A way to obtain the current TPM state can be supplied via the TPMState argument. This is used
// for decisions in automatic branch selection.
//
// The caller may explicitly select branches to execute via the Path argument of
// [PolicyExecuteParams]. Alternatively, if branches are not specified explicitly, appropriate
// branches are selected automatically where possible. This works by selecting the first
// appropriate branch from all of the candidate branches. Inappropriate branches are filtered out
// from all of the candidate branches if any of the following conditions are true:
//   - It contains a command code, command parameter hash, or name hash that doesn't match
//     the supplied [PolicySessionUsage].
//   - It uses TPM2_PolicyPassword or TPM2_PolicyAuthValue when the supplied [PolicySessionUsage]
//     indicates that this can't be used.
//   - It uses TPM2_PolicyNvWritten with a value that doesn't match the public area of the NV index
//     provided via the supplied [PolicySessionUsage].
//   - It uses TPM2_PolicySigned and there is no [PolicySignedAuthorization] or [PolicyTicket]
//     supplied. Note that if either of these are supplied, it is assumed that they will succeed.
//   - It uses TPM2_PolicyPCR with values that don't match the current PCR values.
//   - It uses TPM2_PolicyCounterTimer with conditions that will fail.
//
// Note that when automatically selecting branches, it is assumed that any TPM2_PolicySecret or
// TPM2_PolicyNV assertions will succeed.
//
// On success, the supplied policy session may be used for authorization in a context that requires
// that this policy is satisfied. It will also return a list of tickets generated by any assertions.
func (p *Policy) Execute(session Session, params *PolicyExecuteParams, resources ResourceLoader, state TPMState) ([]*PolicyTicket, error) {
	if session == nil {
		return nil, errors.New("no session")
	}
	if params == nil {
		params = new(PolicyExecuteParams)
	}
	if resources == nil {
		resources = new(nullResourceLoader)
	}
	if state == nil {
		state = new(nullTpmState)
	}

	runner := new(policyRunner)
	runner.policyRunnerContext = newPolicyRunnerContext(
		session,
		newExecutePolicyParams(params),
		resources,
		newExecutePolicyFlowHandler(state, runner, params))

	if err := runner.run(p.policy.Policy); err != nil {
		return nil, err
	}

	var tickets []*PolicyTicket
	for _, ticket := range runner.tickets {
		tickets = append(tickets, ticket)
	}

	return tickets, nil
}

type validatePolicyFlowHandler struct {
	runner *policyRunner
}

func newValidatePolicyFlowHandler(runner *policyRunner) *validatePolicyFlowHandler {
	return &validatePolicyFlowHandler{runner: runner}
}

func (h *validatePolicyFlowHandler) handleBranches(branches policyBranches) error {
	context := &policyBranchNodeContext{
		dispatcher:  h.runner,
		session:     h.runner.policySession,
		flowHandler: h.runner.policyFlowHandler,
		branches:    branches,
	}

	return context.computeBranchDigests(func() error {
		for i := range branches {
			computedDigest := context.digests[i]
			for _, d := range branches[i].PolicyDigests {
				if d.HashAlg != h.runner.session().HashAlg() {
					continue
				}

				if !bytes.Equal(d.Digest, computedDigest) {
					return fmt.Errorf("stored and computed branch digest mismatch (computed: %x, stored: %x)", computedDigest, d.Digest)
				}
			}
		}
		return context.completeBranchNode()
	})
}

func (h *validatePolicyFlowHandler) pushComputeContext(digest *taggedHash) (restore func()) {
	oldContext := h.runner.policyRunnerContext
	h.runner.policyRunnerContext = newPolicyRunnerContext(
		newComputePolicySession(digest),
		oldContext.policyParams,
		oldContext.policyResources,
		oldContext.policyFlowHandler,
	)

	return func() {
		h.runner.policyRunnerContext = oldContext
	}
}

func newValidatePolicyRunnerContext(runner *policyRunner, digest *taggedHash) *policyRunnerContext {
	external := make(map[*tpm2.Public]tpm2.Name)
	return newPolicyRunnerContext(
		newComputePolicySession(digest),
		newMockPolicyParams(external),
		newMockResourceLoader(external),
		newValidatePolicyFlowHandler(runner),
	)
}

// Validate performs some checking of every element in the policy, and
// verifies that every branch is consistent with the stored digests where
// they exist. On success, it returns the digest correpsonding to this policy
// for the specified digest algorithm.
func (p *Policy) Validate(alg tpm2.HashAlgorithmId) (tpm2.Digest, error) {
	digest := &taggedHash{HashAlg: alg, Digest: make(tpm2.Digest, alg.Size())}

	runner := new(policyRunner)
	runner.policyRunnerContext = newValidatePolicyRunnerContext(runner, digest)
	if err := runner.run(p.policy.Policy); err != nil {
		return nil, err
	}

	//for _, d := range p.policy.PolicyDigests {
	//	if d.HashAlg != alg {
	//		continue
	//	}
	//
	//	if !bytes.Equal(d.Digest, digest.Digest) {
	//		return nil, fmt.Errorf("stored and computed policy digest mismatch (computed: %x, stored: %x)", digest.Digest, d.Digest)
	//	}
	//}

	return digest.Digest, nil
}
