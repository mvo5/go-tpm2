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
	"math"
	"reflect"

	"github.com/canonical/go-tpm2"
	"github.com/canonical/go-tpm2/mu"
)

// ErrMissingDigest is returned from [Policy.Execute] when a TPM2_PolicyCpHash or
// TPM2_PolicyNameHash assertion is missing a digest for the selected session algorithm.
var ErrMissingDigest = errors.New("missing digest for session algorithm")

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

// AuthorizationNotFoundError is returned from [Policy.Execute] if the policy required a
// signed authorization for a TPM2_PolicySigned assertion, but one wasn't supplied and
// an appropriate ticket was also not supplied.
type AuthorizationNotFoundError struct {
	AuthName  tpm2.Name
	PolicyRef tpm2.Nonce
}

func (e *AuthorizationNotFoundError) Error() string {
	return fmt.Sprintf("missing signed authorization for assertion with authName: %#x, policyRef: %#x)", e.AuthName, e.PolicyRef)
}

// ResourceNotFoundError is returned from [Policy.Execute] if the policy required a resource
// with the indicated name but one wasn't supplied.
type ResourceNotFoundError tpm2.Name

func (e ResourceNotFoundError) Error() string {
	return fmt.Sprintf("missing resource with name %#x", tpm2.Name(e))
}

// SavedContext contains the context of a saved transient object and its name, and
// can be used to supply transient resources to [Policy.Execute].
type SavedContext struct {
	Name    tpm2.Name
	Context *tpm2.Context
}

// SaveAndFlushResource saves the context of the supplied transient resource, flushes it and
// returns a *SavedContext instance that can be supplied to [Policy.Execute].
func SaveAndFlushResource(tpm *tpm2.TPMContext, resource tpm2.ResourceContext) (*SavedContext, error) {
	name := resource.Name()
	context, err := tpm.ContextSave(resource)
	if err != nil {
		return nil, err
	}
	if err := tpm.FlushContext(resource); err != nil {
		return nil, err
	}
	return &SavedContext{Name: name, Context: context}, nil
}

// LoadableObject contains the data associated with an unloaded transient object, and
// can be used to supply transient resources to [Policy.Execute].
type LoadableObject struct {
	ParentName tpm2.Name
	Public     *tpm2.Public
	Private    tpm2.Private
}

// PolicyResources contains the resources that are required by [Policy.Execute].
type PolicyResources struct {
	// Loaded resources are resources that are already loaded in the TPM, such
	// as NV indices, persistent resources, or transient resources that have
	// already been loaded. Note that permanent or PCR resources do not need
	// to be explicitly supplied.
	Loaded []tpm2.ResourceContext

	// Saved resources are transient objects that have been previously loaded,
	// context saved and then flushed, and need to be context loaded with
	// TPM2_ContextLoad in order to use. These will be flushed after use.
	Saved []*SavedContext

	// Unloaded resources are transient objects that need to be loaded with
	// TPM2_Load in order to use. These will be flushed after use.
	Unloaded []*LoadableObject
}

// PolicyExecuteParams contains parameters that are useful for executing a policy.
type PolicyExecuteParams struct {
	Resources      *PolicyResources       // Resources required by the policy
	SecretParams   []*PolicySecretParams  // Parameters for TPM2_PolicySecret assertions
	Tickets        []*PolicyTicket        // Tickets for TPM2_PolicySecret and TPM2_PolicySigned assertions
	Authorizations []*PolicyAuthorization // Authorizations for TPM2_PolicySigned assertions
}

// PolicyResourceAuthorizer provides a way for an application to authorize resources
// that are used by a policy.
type PolicyResourceAuthorizer interface {
	// Authorize requests that the supplied context is prepared for use with the user auth role for
	// the corresponding resource. If the user auth role requires knowledge of the authorization
	// value, this should be set by the implementation. The implementation can also return an optional
	// session to use for authorization. If no session is returned, passphrase auth is used. Note
	// that the returned session will not be flushed if the AttrContinueSession attribute is set, or
	// an error occurs.
	//
	// This is required to support TPM2_PolicyNV and TPM2_PolicySecret.
	Authorize(resource tpm2.ResourceContext) (tpm2.SessionContext, error)
}

type policySession interface {
	HashAlg() tpm2.HashAlgorithmId

	PolicyNV(auth, index tpm2.ResourceContext, operandB tpm2.Operand, offset uint16, operation tpm2.ArithmeticOp, authAuthSession tpm2.SessionContext) error
	PolicySecret(authObject tpm2.ResourceContext, cpHashA tpm2.Digest, policyRef tpm2.Nonce, expiration int32, authObjectAuthSession tpm2.SessionContext) (tpm2.Timeout, *tpm2.TkAuth, error)
	PolicySigned(authKey tpm2.ResourceContext, includeNonceTPM bool, cpHashA tpm2.Digest, policyRef tpm2.Nonce, expiration int32, auth *tpm2.Signature) (tpm2.Timeout, *tpm2.TkAuth, error)
	PolicyAuthorize(approvedPolicy tpm2.Digest, policyRef tpm2.Nonce, keySign tpm2.Name, verified *tpm2.TkVerified) error
	PolicyAuthValue() error
	PolicyCommandCode(code tpm2.CommandCode) error
	PolicyCounterTimer(operandB tpm2.Operand, offset uint16, operation tpm2.ArithmeticOp) error
	PolicyCpHash(cpHashA tpm2.Digest) error
	PolicyNameHash(nameHash tpm2.Digest) error
	PolicyOR(pHashList tpm2.DigestList) error
	PolicyTicket(timeout tpm2.Timeout, cpHashA tpm2.Digest, policyRef tpm2.Nonce, authName tpm2.Name, ticket *tpm2.TkAuth) error
	PolicyPCR(pcrDigest tpm2.Digest, pcrs tpm2.PCRSelectionList) error
	PolicyDuplicationSelect(objectName, newParentName tpm2.Name, includeObject bool) error
	PolicyPassword() error
	PolicyNvWritten(writtenSet bool) error
}

type policyParams interface {
	secretParams(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySecretParams
	signedAuthorization(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyAuthorization
	ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket
}

type policyResourceContext interface {
	resource() tpm2.ResourceContext
	flush() error
}

type policyResources interface {
	loadHandle(handle tpm2.Handle) (tpm2.ResourceContext, error)
	loadName(name tpm2.Name) (policyResourceContext, error)
	loadExternal(pub *tpm2.Public) (policyResourceContext, error)

	nvReadPublic(context tpm2.HandleContext) (*tpm2.NVPublic, error)
	authorize(context tpm2.ResourceContext) (tpm2.SessionContext, error)
}

type policyRunContext interface {
	session() policySession
	params() policyParams
	resources() policyResources

	ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket
	addTicket(ticket *PolicyTicket)
}

type realPolicySession struct {
	tpm           *tpm2.TPMContext
	policySession tpm2.SessionContext
	authorizer    PolicyResourceAuthorizer
	sessions      []tpm2.SessionContext
}

func newRealPolicySession(tpm *tpm2.TPMContext, policySession tpm2.SessionContext, sessions ...tpm2.SessionContext) *realPolicySession {
	return &realPolicySession{
		tpm:           tpm,
		policySession: policySession,
		sessions:      sessions}
}

func (s *realPolicySession) HashAlg() tpm2.HashAlgorithmId {
	return s.policySession.HashAlg()
}

func (s *realPolicySession) PolicyNV(auth, index tpm2.ResourceContext, operandB tpm2.Operand, offset uint16, operation tpm2.ArithmeticOp, authAuthSession tpm2.SessionContext) error {
	return s.tpm.PolicyNV(auth, index, s.policySession, operandB, offset, operation, authAuthSession, s.sessions...)
}

func (s *realPolicySession) PolicySecret(authObject tpm2.ResourceContext, cpHashA tpm2.Digest, policyRef tpm2.Nonce, expiration int32, authObjectAuthSession tpm2.SessionContext) (tpm2.Timeout, *tpm2.TkAuth, error) {
	return s.tpm.PolicySecret(authObject, s.policySession, cpHashA, policyRef, expiration, authObjectAuthSession, s.sessions...)
}

func (s *realPolicySession) PolicySigned(authKey tpm2.ResourceContext, includeNonceTPM bool, cpHashA tpm2.Digest, policyRef tpm2.Nonce, expiration int32, auth *tpm2.Signature) (tpm2.Timeout, *tpm2.TkAuth, error) {
	return s.tpm.PolicySigned(authKey, s.policySession, includeNonceTPM, cpHashA, policyRef, expiration, auth, s.sessions...)
}

func (s *realPolicySession) PolicyAuthorize(approvedPolicy tpm2.Digest, policyRef tpm2.Nonce, keySign tpm2.Name, verified *tpm2.TkVerified) error {
	return s.tpm.PolicyAuthorize(s.policySession, approvedPolicy, policyRef, keySign, verified, s.sessions...)
}

func (s *realPolicySession) PolicyAuthValue() error {
	return s.tpm.PolicyAuthValue(s.policySession, s.sessions...)
}

func (s *realPolicySession) PolicyCommandCode(code tpm2.CommandCode) error {
	return s.tpm.PolicyCommandCode(s.policySession, code, s.sessions...)
}

func (s *realPolicySession) PolicyCounterTimer(operandB tpm2.Operand, offset uint16, operation tpm2.ArithmeticOp) error {
	return s.tpm.PolicyCounterTimer(s.policySession, operandB, offset, operation, s.sessions...)
}

func (s *realPolicySession) PolicyCpHash(cpHashA tpm2.Digest) error {
	return s.tpm.PolicyCpHash(s.policySession, cpHashA, s.sessions...)
}

func (s *realPolicySession) PolicyNameHash(nameHash tpm2.Digest) error {
	return s.tpm.PolicyNameHash(s.policySession, nameHash, s.sessions...)
}

func (s *realPolicySession) PolicyOR(pHashList tpm2.DigestList) error {
	return s.tpm.PolicyOR(s.policySession, pHashList, s.sessions...)
}

func (s *realPolicySession) PolicyTicket(timeout tpm2.Timeout, cpHashA tpm2.Digest, policyRef tpm2.Nonce, authName tpm2.Name, ticket *tpm2.TkAuth) error {
	return s.tpm.PolicyTicket(s.policySession, timeout, cpHashA, policyRef, authName, ticket, s.sessions...)
}

func (s *realPolicySession) PolicyPCR(pcrDigest tpm2.Digest, pcrs tpm2.PCRSelectionList) error {
	return s.tpm.PolicyPCR(s.policySession, pcrDigest, pcrs, s.sessions...)
}

func (s *realPolicySession) PolicyDuplicationSelect(objectName, newParentName tpm2.Name, includeObject bool) error {
	return s.tpm.PolicyDuplicationSelect(s.policySession, objectName, newParentName, includeObject, s.sessions...)
}

func (s *realPolicySession) PolicyPassword() error {
	return s.tpm.PolicyPassword(s.policySession, s.sessions...)
}

func (s *realPolicySession) PolicyNvWritten(writtenSet bool) error {
	return s.tpm.PolicyNvWritten(s.policySession, writtenSet, s.sessions...)
}

type realPolicyParams struct {
	policySecretParams map[paramKey]*PolicySecretParams
	authorizations     map[paramKey]*PolicyAuthorization
	tickets            map[paramKey]*PolicyTicket
}

func newRealPolicyParams(params *PolicyExecuteParams) *realPolicyParams {
	out := &realPolicyParams{
		policySecretParams: make(map[paramKey]*PolicySecretParams),
		authorizations:     make(map[paramKey]*PolicyAuthorization),
		tickets:            make(map[paramKey]*PolicyTicket),
	}
	for _, param := range params.SecretParams {
		out.policySecretParams[policyParamKey(param.AuthName, param.PolicyRef)] = param
	}
	for _, ticket := range params.Tickets {
		out.tickets[policyParamKey(ticket.AuthName, ticket.PolicyRef)] = ticket
	}
	for _, auth := range params.Authorizations {
		out.authorizations[policyParamKey(auth.AuthName, auth.PolicyRef)] = auth
	}

	return out
}

func (p *realPolicyParams) secretParams(authName tpm2.Name, policyRef tpm2.Nonce) *PolicySecretParams {
	return p.policySecretParams[policyParamKey(authName, policyRef)]
}

func (p *realPolicyParams) signedAuthorization(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyAuthorization {
	return p.authorizations[policyParamKey(authName, policyRef)]
}

func (p *realPolicyParams) ticket(authName tpm2.Name, policyRef tpm2.Nonce) *PolicyTicket {
	return p.tickets[policyParamKey(authName, policyRef)]
}

type policyResourceContextFlushable struct {
	rc  tpm2.ResourceContext
	tpm *tpm2.TPMContext
}

func newPolicyResourceContextFlushable(tpm *tpm2.TPMContext, context tpm2.ResourceContext) policyResourceContext {
	return &policyResourceContextFlushable{rc: context, tpm: tpm}
}

func (r *policyResourceContextFlushable) resource() tpm2.ResourceContext {
	return r.rc
}

func (r *policyResourceContextFlushable) flush() error {
	return r.tpm.FlushContext(r.rc)
}

type policyResourceContextNoFlush struct {
	rc tpm2.ResourceContext
}

func newPolicyResourceContextNonFlushable(context tpm2.ResourceContext) policyResourceContext {
	return &policyResourceContextNoFlush{rc: context}
}

func (r *policyResourceContextNoFlush) resource() tpm2.ResourceContext {
	return r.rc
}

func (r *policyResourceContextNoFlush) flush() error {
	return nil
}

type realPolicyResources struct {
	tpm        *tpm2.TPMContext
	loaded     []tpm2.ResourceContext
	saved      []*SavedContext
	unloaded   []*LoadableObject
	authorizer PolicyResourceAuthorizer
	sessions   []tpm2.SessionContext
}

func newRealPolicyResources(tpm *tpm2.TPMContext, resources *PolicyResources, authorizer PolicyResourceAuthorizer, sessions ...tpm2.SessionContext) *realPolicyResources {
	if resources == nil {
		resources = new(PolicyResources)
	}

	return &realPolicyResources{
		tpm:        tpm,
		loaded:     resources.Loaded,
		saved:      resources.Saved,
		unloaded:   resources.Unloaded,
		authorizer: authorizer,
		sessions:   sessions,
	}
}

func (r *realPolicyResources) loadHandle(handle tpm2.Handle) (tpm2.ResourceContext, error) {
	switch handle.Type() {
	case tpm2.HandleTypePCR, tpm2.HandleTypePermanent:
		return r.tpm.GetPermanentContext(handle), nil
	case tpm2.HandleTypeNVIndex:
		return r.tpm.NewResourceContext(handle, r.sessions...)
	default:
		return nil, fmt.Errorf("invalid handle type %v", handle.Type())
	}
}

func (r *realPolicyResources) loadName(name tpm2.Name) (policyResourceContext, error) {
	if !name.IsValid() {
		return nil, errors.New("invalid name")
	}
	if name.Type() == tpm2.NameTypeHandle && (name.Handle().Type() == tpm2.HandleTypePCR || name.Handle().Type() == tpm2.HandleTypePermanent) {
		return newPolicyResourceContextNonFlushable(r.tpm.GetPermanentContext(name.Handle())), nil
	}

	// Search already loaded resources
	for _, resource := range r.loaded {
		if !bytes.Equal(resource.Name(), name) {
			continue
		}

		return newPolicyResourceContextNonFlushable(resource), nil
	}

	// Search saved contexts
	for _, context := range r.saved {
		if !bytes.Equal(context.Name, name) {
			continue
		}

		hc, err := r.tpm.ContextLoad(context.Context)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(hc.Name(), name) {
			r.tpm.FlushContext(hc)
			return nil, fmt.Errorf("loaded context has the wrong name (got %#x, expected %#x)", hc.Name(), name)
		}
		resource, ok := hc.(tpm2.ResourceContext)
		if !ok {
			r.tpm.FlushContext(hc)
			return nil, fmt.Errorf("name %#x associated with a context of the wrong type", name)
		}

		return newPolicyResourceContextFlushable(r.tpm, resource), nil
	}

	// Search loadable objects
	for _, object := range r.unloaded {
		if !bytes.Equal(object.Public.Name(), name) {
			continue
		}

		parent, err := r.loadName(object.ParentName)
		if err != nil {
			return nil, fmt.Errorf("cannot load parent for object with name %#x: %w", name, err)
		}
		defer parent.flush()

		session, err := r.authorize(parent.resource())
		if err != nil {
			return nil, fmt.Errorf("cannot authorize parent with name %#x: %w", parent.resource().Name(), err)
		}

		resource, err := r.tpm.Load(parent.resource(), object.Private, object.Public, session, r.sessions...)
		if err != nil {
			return nil, err
		}

		if context, err := r.tpm.ContextSave(resource); err == nil {
			r.saved = append(r.saved, &SavedContext{Name: name, Context: context})
		}

		return newPolicyResourceContextFlushable(r.tpm, resource), nil
	}

	// Search persistent and NV index handles
	handles, err := r.tpm.GetCapabilityHandles(tpm2.HandleTypePersistent.BaseHandle(), math.MaxUint32, r.sessions...)
	if err != nil {
		return nil, err
	}
	nvHandles, err := r.tpm.GetCapabilityHandles(tpm2.HandleTypeNVIndex.BaseHandle(), math.MaxUint32, r.sessions...)
	if err != nil {
		return nil, err
	}
	handles = append(handles, nvHandles...)
	for _, handle := range handles {
		resource, err := r.tpm.NewResourceContext(handle, r.sessions...)
		if tpm2.IsResourceUnavailableError(err, handle) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(resource.Name(), name) {
			continue
		}

		r.loaded = append(r.loaded, resource)
		return newPolicyResourceContextNonFlushable(resource), nil
	}

	return nil, ResourceNotFoundError(name)
}

func (r *realPolicyResources) loadExternal(public *tpm2.Public) (policyResourceContext, error) {
	rc, err := r.tpm.LoadExternal(nil, public, tpm2.HandleOwner, r.sessions...)
	if err != nil {
		return nil, err
	}
	return newPolicyResourceContextFlushable(r.tpm, rc), nil
}

func (r *realPolicyResources) nvReadPublic(context tpm2.HandleContext) (*tpm2.NVPublic, error) {
	pub, _, err := r.tpm.NVReadPublic(context)
	return pub, err
}

func (r *realPolicyResources) authorize(context tpm2.ResourceContext) (tpm2.SessionContext, error) {
	if r.authorizer == nil {
		return nil, errors.New("no authorizer")
	}
	return r.authorizer.Authorize(context)
}

type policyElementRunner interface {
	name() string
	run(context policyRunContext) error
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
	NvIndex   tpm2.Handle
	OperandB  tpm2.Operand
	Offset    uint16
	Operation tpm2.ArithmeticOp
}

func (*policyNV) name() string { return "TPM2_PolicyNV assertion" }

func (e *policyNV) run(context policyRunContext) error {
	nvIndex, err := context.resources().loadHandle(e.NvIndex)
	if err != nil {
		return fmt.Errorf("cannot create nvIndex context: %w", err)
	}

	pub, err := context.resources().nvReadPublic(nvIndex)
	if err != nil {
		return fmt.Errorf("cannot read nvIndex public area: %w", err)
	}

	auth := nvIndex
	switch {
	default:
	case pub.Attrs&tpm2.AttrNVOwnerRead != 0:
		auth, err = context.resources().loadHandle(tpm2.HandleOwner)
	case pub.Attrs&tpm2.AttrNVPPRead != 0:
		auth, err = context.resources().loadHandle(tpm2.HandlePlatform)
	}
	if err != nil {
		return fmt.Errorf("cannot create auth context: %w", err)
	}

	authContextAuthSession, err := context.resources().authorize(auth)
	if err != nil {
		return fmt.Errorf("cannot authorize auth object: %w", err)
	}

	return context.session().PolicyNV(auth, nvIndex, e.OperandB, e.Offset, e.Operation, authContextAuthSession)
}

type policySecret struct {
	AuthObjectName tpm2.Name
	PolicyRef      tpm2.Nonce
}

func (*policySecret) name() string { return "TPM2_PolicySecret assertion" }

func (e *policySecret) run(context policyRunContext) error {
	if ticket := context.ticket(e.AuthObjectName, e.PolicyRef); ticket != nil {
		err := context.session().PolicyTicket(ticket.Timeout, ticket.CpHash, ticket.PolicyRef, ticket.AuthName, ticket.Ticket)
		switch {
		case tpm2.IsTPMParameterError(err, tpm2.ErrorExpired, tpm2.CommandPolicyTicket, 1):
			// The ticket has expired - ignore this and fall through to PolicySecret
		case tpm2.IsTPMParameterError(err, tpm2.ErrorTicket, tpm2.CommandPolicyTicket, 5):
			// The ticket is invalid - ignore this and fall through to PolicySecret
		case err != nil:
			return err
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

	authObject, err := context.resources().loadName(e.AuthObjectName)
	if err != nil {
		return fmt.Errorf("cannot create authObject context: %w", err)
	}
	defer authObject.flush()

	authObjectAuthSession, err := context.resources().authorize(authObject.resource())
	if err != nil {
		return fmt.Errorf("cannot authorize authObject: %w", err)
	}

	timeout, ticket, err := context.session().PolicySecret(authObject.resource(), cpHashA, e.PolicyRef, params.Expiration, authObjectAuthSession)
	if err != nil {
		return err
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
	AuthKey   *tpm2.Public
	PolicyRef tpm2.Nonce
}

func (*policySigned) name() string { return "TPM2_PolicySigned assertion" }

func (e *policySigned) run(context policyRunContext) error {
	authKey, err := context.resources().loadExternal(e.AuthKey)
	if err != nil {
		return fmt.Errorf("cannot create authKey context: %w", err)
	}
	defer authKey.flush()

	if ticket := context.ticket(authKey.resource().Name(), e.PolicyRef); ticket != nil {
		err := context.session().PolicyTicket(ticket.Timeout, ticket.CpHash, ticket.PolicyRef, ticket.AuthName, ticket.Ticket)
		switch {
		case tpm2.IsTPMParameterError(err, tpm2.ErrorExpired, tpm2.CommandPolicyTicket, 1):
			// The ticket has expired - ignore this and fall through to PolicySigned
		case tpm2.IsTPMParameterError(err, tpm2.ErrorTicket, tpm2.CommandPolicyTicket, 5):
			// The ticket is invalid - ignore this and fall through to PolicySigned
		case err != nil:
			return err
		default:
			// The ticket was accepted
			return nil
		}
	}

	auth := context.params().signedAuthorization(authKey.resource().Name(), e.PolicyRef)
	if auth == nil {
		return &AuthorizationNotFoundError{AuthName: authKey.resource().Name(), PolicyRef: e.PolicyRef}
	}

	includeNonceTPM := false
	if len(auth.NonceTPM) > 0 {
		includeNonceTPM = true
	}

	timeout, ticket, err := context.session().PolicySigned(authKey.resource(), includeNonceTPM, auth.CpHash, e.PolicyRef, auth.Expiration, auth.Signature)
	if err != nil {
		return err
	}

	context.addTicket(&PolicyTicket{
		AuthName:  authKey.resource().Name(),
		PolicyRef: e.PolicyRef,
		CpHash:    auth.CpHash,
		Timeout:   timeout,
		Ticket:    ticket})
	return nil
}

type policyAuthValue struct{}

func (*policyAuthValue) name() string { return "TPM2_PolicyAuthValue assertion" }

func (*policyAuthValue) run(context policyRunContext) error {
	return context.session().PolicyAuthValue()
}

type policyCommandCode struct {
	CommandCode tpm2.CommandCode
}

func (*policyCommandCode) name() string { return "TPM2_PolicyCommandCode assertion" }

func (e *policyCommandCode) run(context policyRunContext) error {
	return context.session().PolicyCommandCode(e.CommandCode)
}

type policyCounterTimer struct {
	OperandB  tpm2.Operand
	Offset    uint16
	Operation tpm2.ArithmeticOp
}

func (*policyCounterTimer) name() string { return "TPM2_PolicyCounterTimer assertion" }

func (e *policyCounterTimer) run(context policyRunContext) error {
	return context.session().PolicyCounterTimer(e.OperandB, e.Offset, e.Operation)
}

type policyCpHash struct {
	Digests taggedHashList
}

func (*policyCpHash) name() string { return "TPM2_PolicyCpHash assertion" }

func (e *policyCpHash) run(context policyRunContext) error {
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

func (e *policyNameHash) run(context policyRunContext) error {
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

type pcrValue struct {
	PCR    tpm2.Handle
	Digest taggedHash
}

type pcrValueList []pcrValue

type policyPCR struct {
	PCRs pcrValueList
}

func (*policyPCR) name() string { return "TPM2_PolicyPCR assertion" }

func (e *policyPCR) run(context policyRunContext) error {
	values := make(tpm2.PCRValues)
	for i, value := range e.PCRs {
		if value.PCR.Type() != tpm2.HandleTypePCR {
			return fmt.Errorf("invalid PCR handle at index %d", i)
		}
		if err := values.SetValue(value.Digest.HashAlg, int(value.PCR), value.Digest.Digest); err != nil {
			return fmt.Errorf("invalid PCR value at index %d: %w", i, err)
		}
	}
	pcrs, pcrDigest, err := ComputePCRDigestFromAllValues(context.session().HashAlg(), values)
	if err != nil {
		return fmt.Errorf("cannot compute PCR digest: %w", err)
	}
	return context.session().PolicyPCR(pcrDigest, pcrs)
}

type policyDuplicationSelect struct {
	Object        tpm2.Name
	NewParent     tpm2.Name
	IncludeObject bool
}

func (*policyDuplicationSelect) name() string { return "TPM2_PolicyDuplicationSelect assertion" }

func (e *policyDuplicationSelect) run(context policyRunContext) error {
	return context.session().PolicyDuplicationSelect(e.Object, e.NewParent, e.IncludeObject)
}

type policyPassword struct{}

func (*policyPassword) name() string { return "TPM2_PolicyPassword assertion" }

func (*policyPassword) run(context policyRunContext) error {
	return context.session().PolicyPassword()
}

type policyNvWritten struct {
	WrittenSet bool
}

func (*policyNvWritten) name() string { return "TPM2_PolicyNvWritten assertion" }

func (e *policyNvWritten) run(context policyRunContext) error {
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
	PCR               *policyPCR
	DuplicationSelect *policyDuplicationSelect
	Password          *policyPassword
	NvWritten         *policyNvWritten
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
	case tpm2.CommandPolicyPCR:
		return &d.PCR
	case tpm2.CommandPolicyDuplicationSelect:
		return &d.DuplicationSelect
	case tpm2.CommandPolicyPassword:
		return &d.Password
	case tpm2.CommandPolicyNvWritten:
		return &d.NvWritten
	default:
		return nil
	}
}

type policyElement struct {
	Type    tpm2.CommandCode
	Details *policyElementDetails
}

func (e *policyElement) runner() policyElementRunner {
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
	case tpm2.CommandPolicyPCR:
		return e.Details.PCR
	case tpm2.CommandPolicyDuplicationSelect:
		return e.Details.DuplicationSelect
	case tpm2.CommandPolicyPassword:
		return e.Details.Password
	case tpm2.CommandPolicyNvWritten:
		return e.Details.NvWritten
	default:
		panic("invalid type")
	}
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

type policyRunner struct {
	policySession   policySession
	policyParams    policyParams
	policyResources policyResources

	tickets map[paramKey]*PolicyTicket

	elements []policyElementRunner
}

func newPolicyRunner(session policySession, params policyParams, resources policyResources) *policyRunner {
	return &policyRunner{
		policySession:   session,
		policyParams:    params,
		policyResources: resources,
		tickets:         make(map[paramKey]*PolicyTicket),
	}
}

func (r *policyRunner) session() policySession {
	return r.policySession
}

func (r *policyRunner) params() policyParams {
	return r.policyParams
}

func (r *policyRunner) resources() policyResources {
	return r.policyResources
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

func (r *policyRunner) run(policy policy) error {
	var elements []policyElementRunner
	for _, element := range policy.Policy {
		elements = append(elements, element.runner())
	}
	r.elements = elements

	for len(r.elements) > 0 {
		element := r.elements[0]
		r.elements = r.elements[1:]

		if err := element.run(r); err != nil {
			return fmt.Errorf("cannot process %s: %w", element.name(), err)
		}
	}

	return nil
}

// Execute runs this policy using the supplied TPM context and on the supplied policy session.
//
// The caller may supply additional parameters via the PolicyExecuteParams struct. This can contain
// parameters for TPM2_PolicySecret assertions, signed authorizations for TPM2_PolicySigned
// assertions, or tickets to satisfy TPM2_PolicySecret or TPM2_PolicySigned assertions. Each of
// these parameters are associated with a policy assertion by a name and policy reference.
//
// Resources required by a policy are also supplied via the PolicyExecuteParams struct. These can
// either be supplied as already loaded resources, or saved contexts or unloaded objects for resources
// that are transient objects. If a resource can not be found in the parameters supplied, this
// function will search the TPM's active persistent and NV index handles.
//
// Some policy assertions require authorization with the user auth role for some resources. An
// implementation of PolicyResourceAuthorizer must be supplied for this.
//
// On success, the supplied policy session may be used for authorization in a context that requires
// that this policy is satisfied. It will also return a list of tickets generated by any assertions.
func (p *Policy) Execute(tpm *tpm2.TPMContext, policySession tpm2.SessionContext, params *PolicyExecuteParams, authorizer PolicyResourceAuthorizer, sessions ...tpm2.SessionContext) ([]*PolicyTicket, error) {
	if params == nil {
		params = new(PolicyExecuteParams)
	}

	runner := newPolicyRunner(
		newRealPolicySession(tpm, policySession, sessions...),
		newRealPolicyParams(params),
		newRealPolicyResources(tpm, params.Resources, authorizer, sessions...),
	)

	if err := runner.run(p.policy); err != nil {
		return nil, err
	}

	var tickets []*PolicyTicket
	for _, ticket := range runner.tickets {
		tickets = append(tickets, ticket)
	}

	return tickets, nil
}
