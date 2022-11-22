// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"sort"

	"github.com/canonical/go-tpm2/mu"
)

// This file contains types defined in section 10 (Structures) in
// part 2 of the library spec.

type Empty struct{}

// 10.3) Hash/Digest structures

// TaggedHash corresponds to the TPMT_HA type.
type TaggedHash struct {
	HashAlg HashAlgorithmId // Algorithm of the digest contained with Digest
	Digest  []byte          // Digest data
}

// TaggedHash represents the TPMT_HA type in the TCG spec. In the spec, TPMT_HA.digest is a union type
// (TPMU_HA), which is a union of all of the different hash algorithms. Each member of that union is an
// array of raw bytes. As no length is encoded, we need a custom marshaller implementation that unmarshals the
// correct number of bytes depending on the hash algorithm

func (p TaggedHash) Marshal(w io.Writer) error {
	if err := binary.Write(w, binary.BigEndian, p.HashAlg); err != nil {
		return fmt.Errorf("cannot marshal digest algorithm: %w", err)
	}
	if !p.HashAlg.IsValid() {
		return fmt.Errorf("cannot determine digest size for unknown algorithm %v", p.HashAlg)
	}

	if p.HashAlg.Size() != len(p.Digest) {
		return fmt.Errorf("invalid digest size %d", len(p.Digest))
	}

	if _, err := w.Write(p.Digest); err != nil {
		return fmt.Errorf("cannot write digest: %w", err)
	}
	return nil
}

func (p *TaggedHash) Unmarshal(r io.Reader) error {
	if err := binary.Read(r, binary.BigEndian, &p.HashAlg); err != nil {
		return fmt.Errorf("cannot unmarshal digest algorithm: %w", err)
	}
	if !p.HashAlg.IsValid() {
		return fmt.Errorf("cannot determine digest size for unknown algorithm %v", p.HashAlg)
	}

	p.Digest = make(Digest, p.HashAlg.Size())
	if _, err := io.ReadFull(r, p.Digest); err != nil {
		return fmt.Errorf("cannot read digest: %w", err)
	}
	return nil
}

// 10.4 Sized Buffers

// Digest corresponds to the TPM2B_DIGEST type. The largest size of this supported
// by the TPM can be determined by calling TPMContext.GetMaxDigest.
type Digest []byte

// Data corresponds to the TPM2B_DATA type. The largest size of this supported by
// the TPM can be determined by calling TPMContext.GetMaxData.
type Data []byte

// Nonce corresponds to the TPM2B_NONCE type.
type Nonce Digest

// Auth corresponds to the TPM2B_AUTH type.
type Auth Digest

// Operand corresponds to the TPM2B_OPERAND type.
type Operand Digest

const (
	// EventMaxSize indicates the maximum size of arguments of the Event type.
	EventMaxSize = 1024
)

// Event corresponds to the TPM2B_EVENT type. The largest size of this is indicated
// by EventMaxSize.
type Event []byte

// MaxBuffer corresponds to the TPM2B_MAX_BUFFER type. The largest size of this supported
// by the TPM can be determined by calling TPMContext.GetInputBuffer.
type MaxBuffer []byte

// MaxNVBuffer corresponds to the TPM2B_MAX_NV_BUFFER type. The largest size of this
// supported by the TPM can be determined by calling TPMContext.GetNVBufferMax.
type MaxNVBuffer []byte

// Timeout corresponds to the TPM2B_TIMEOUT type. The spec defines this
// as having a maximum size of 8 bytes. It is always 8 bytes in the
// reference implementation and so could be represented as a uint64,
// but we have to preserve the original buffer because there is no
// guarantees that it is always 8 bytes, and the actual TPM buffer
// must be recreated accurately in order for ticket validation to
// work correctly in TPMContext.PolicyTicket.
type Timeout []byte

// Value returns the value as a uint64. The spec defines the TPM2B_TIMEOUT
// type as having a size of up to 8 bytes. If an implementation creates a
// larger value then the result of this is undefined.
func (t Timeout) Value() uint64 {
	return new(big.Int).SetBytes(t).Uint64()
}

// 10.5) Names

// Name corresponds to the TPM2B_NAME type.
type Name []byte

// NameType describes the type of a name.
type NameType int

const (
	// NameTypeInvalid means that a Name is invalid.
	NameTypeInvalid NameType = iota

	// NameTypeHandle means that a Name is a handle.
	NameTypeHandle

	// NameTypeDigest means that a Name is a digest.
	NameTypeDigest
)

// Type determines the type of this name.
func (n Name) Type() NameType {
	if len(n) < binary.Size(HashAlgorithmId(0)) {
		return NameTypeInvalid
	}
	if len(n) == binary.Size(Handle(0)) {
		return NameTypeHandle
	}

	alg := HashAlgorithmId(binary.BigEndian.Uint16(n))
	if !alg.IsValid() {
		return NameTypeInvalid
	}

	if len(n)-binary.Size(HashAlgorithmId(0)) != alg.Size() {
		return NameTypeInvalid
	}

	return NameTypeDigest
}

// Handle returns the handle of the resource that this name corresponds to. If
// Type does not return NameTypeHandle, it will panic.
func (n Name) Handle() Handle {
	if n.Type() != NameTypeHandle {
		panic("name is not a handle")
	}
	return Handle(binary.BigEndian.Uint32(n))
}

// Algorithm returns the digest algorithm of this name. If Type does not return
// NameTypeDigest, it will return HashAlgorithmNull.
func (n Name) Algorithm() HashAlgorithmId {
	if n.Type() != NameTypeDigest {
		return HashAlgorithmNull
	}

	return HashAlgorithmId(binary.BigEndian.Uint16(n))
}

// Digest returns the name as a digest without the algorithm identifier. If
// Type does not return NameTypeDigest, it will panic.
func (n Name) Digest() Digest {
	if n.Type() != NameTypeDigest {
		panic("name is not a valid digest")
	}
	return Digest(n[binary.Size(HashAlgorithmId(0)):])
}

// 10.6) PCR Structures

// PCRSelectBitmap correspnds to the TPMS_PCR_SELECT type, and is a bitmap
// that defines a selection of PCRs. Note that it is easier to work with the
// PCRSelect type instead, which is a slice of PCR indexes.
type PCRSelectBitmap []byte

// ToPCRs converts this PCRSelectBitmap to a slice of PCR indexes.
func (b PCRSelectBitmap) ToPCRs() (out PCRSelect) {
	for i, octet := range b {
		for bit := uint(0); bit < 8; bit++ {
			if octet&(1<<bit) == 0 {
				continue
			}
			out = append(out, int((uint(i)*8)+bit))
		}
	}

	return out
}

func (b PCRSelectBitmap) Marshal(w io.Writer) error {
	if len(b) > math.MaxUint8 {
		return errors.New("bitmap too long")
	}

	if err := binary.Write(w, binary.BigEndian, uint8(len(b))); err != nil {
		return fmt.Errorf("cannot write size of bitmap: %w", err)
	}

	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("cannot write bitmap: %w", err)
	}

	return nil
}

func (b *PCRSelectBitmap) Unmarshal(r io.Reader) error {
	var size uint8
	if err := binary.Read(r, binary.BigEndian, &size); err != nil {
		return fmt.Errorf("cannot read size of bitmap: %w", err)
	}

	*b = make(PCRSelectBitmap, size)

	if _, err := io.ReadFull(r, *b); err != nil {
		return fmt.Errorf("cannot read bitmap: %w", err)
	}

	return nil
}

// PCRSelect is a slice of PCR indexes. It makes it easier to work with the
// TPMS_PCR_SELECT type, which is a bitmap of PCR indices.
//
// It is marshalled to and from the TPMS_PCR_SELECT type for legacy purposes.
// It should be converted to and from PCRSelectBitmap for marshalling, which
// makes it possible to specify the minimum size of the bitmap.
type PCRSelect []int

// ToBitmap converts this PCRSelect into its bitmap form, with the specified
// minimum size. If minsize is zero, a value of 3 will be used which aligns
// with PC client TPM devices.
func (d PCRSelect) ToBitmap(minsize uint8) (out PCRSelectBitmap, err error) {
	if minsize == 0 {
		minsize = 3
	}
	out = make([]byte, minsize)

	for _, i := range d {
		if i < 0 {
			return nil, errors.New("invalid PCR index (< 0)")
		}

		octet := i / 8
		if octet >= math.MaxUint8 {
			return nil, errors.New("invalid PCR index (> 2040)")
		}

		for octet >= len(out) {
			out = append(out, byte(0))
		}
		bit := uint(i % 8)
		out[octet] |= 1 << bit
	}

	return out, nil
}

func (d PCRSelect) Marshal(w io.Writer) error {
	bmp, err := d.ToBitmap(0)
	if err != nil {
		return err
	}
	_, err = mu.MarshalToWriter(w, bmp)
	return err
}

func (d *PCRSelect) Unmarshal(r io.Reader) error {
	var b PCRSelectBitmap
	if _, err := mu.UnmarshalFromReader(r, &b); err != nil {
		return err
	}
	*d = b.ToPCRs()
	return nil
}

// PCRSelection corresponds to the TPMS_PCR_SELECTION type.
type PCRSelection struct {
	Hash   HashAlgorithmId // Hash is the digest algorithm associated with the selection
	Select PCRSelect       // The selected PCRs

	// SizeOfSelect sets the minimum number of bytes in the serialized Select field
	// during marshalling, and is set to the actual number of bytes in the Select
	// field during unmarshalling.
	//
	// TPMs define a minimum size for a PCR selection, based on the number of PCRs
	// defined in its associated platform specification. Note that methods of
	// TPMContext that accept a PCRSelection will set this automatically.
	//
	// If set to zero during marshalling, a value of 3 will be assumed, which
	// aligns with PC client TPM devices.
	SizeOfSelect uint8
}

func (s PCRSelection) Marshal(w io.Writer) error {
	bmp, err := s.Select.ToBitmap(s.SizeOfSelect)
	if err != nil {
		return err
	}
	_, err = mu.MarshalToWriter(w, s.Hash, bmp)
	return err
}

func (s *PCRSelection) Unmarshal(r io.Reader) error {
	var b PCRSelectBitmap
	if _, err := mu.UnmarshalFromReader(r, &s.Hash, &b); err != nil {
		return err
	}
	s.Select = b.ToPCRs()
	s.SizeOfSelect = uint8(len(b))
	return nil
}

// 10.7 Tickets

// TkCreation corresponds to the TPMT_TK_CREATION type. It is created by TPMContext.Create
// and TPMContext.CreatePrimary, and is used to cryptographically bind the CreationData to
// the created object.
type TkCreation struct {
	Tag       StructTag // Ticket structure tag (TagCreation)
	Hierarchy Handle    // The hierarchy of the object to which this ticket belongs.
	Digest    Digest    // HMAC computed using the proof value of Hierarchy
}

// TkVerified corresponds to the TPMT_TK_VERIFIED type. It is created by TPMContext.VerifySignature
// and provides evidence that the TPM has verified that a digest was signed by a specific key.
type TkVerified struct {
	Tag       StructTag // Ticket structure tag (TagVerified)
	Hierarchy Handle    // The hierarchy of the object to which this ticket belongs.
	Digest    Digest    // HMAC computed using the proof value of Hierarcht
}

// TkAuth corresponds to the TPMT_TK_AUTH type. It is created by TPMContext.PolicySigned
// and TPMContext.PolicySecret when the authorization has an expiration time.
type TkAuth struct {
	Tag       StructTag // Ticket structure tag (TagAuthSecret or TagAuthSigned)
	Hierarchy Handle    // The hierarchy of the object used to produce this ticket
	Digest    Digest    // HMAC computed using the proof value of Hierarchy
}

// TkHashcheck corresponds to the TPMT_TK_HASHCHECK type.
type TkHashcheck struct {
	Tag       StructTag // Ticket structure tag (TagHashcheck)
	Hierarchy Handle    // The hierarchy of the object used to produce this ticket
	Digest    Digest    // HMAC computed using the proof value of Hierarchy
}

// AlgorithmProperty corresponds to the TPMS_ALG_PROPERTY type. It is used to report
// the properties of an algorithm.
type AlgorithmProperty struct {
	Alg        AlgorithmId         // Algorithm identifier
	Properties AlgorithmAttributes // Attributes of the algorithm
}

// TaggedProperty corresponds to the TPMS_TAGGED_PROPERTY type. It is used to report
// the value of a property.
type TaggedProperty struct {
	Property Property // Property identifier
	Value    uint32   // Value of the property
}

// TaggedPCRSelect corresponds to the TPMS_TAGGED_PCR_SELECT type. It is used to
// report the PCR indexes associated with a property.
type TaggedPCRSelect struct {
	Tag    PropertyPCR // Property identifier
	Select PCRSelect   // PCRs associated with Tag
}

// TaggedPolicy corresponds to the TPMS_TAGGED_POLICY type. It is used to report
// the authorization policy for a permanent resource.
type TaggedPolicy struct {
	Handle     Handle     // Permanent handle
	PolicyHash TaggedHash // Policy algorithm and hash
}

// 10.9) Lists

// CommandCodeList is a slice of CommandCode values, and corresponds to the TPML_CC type.
type CommandCodeList []CommandCode

// CommandAttributesList is a slice of CommandAttribute values, and corresponds to the TPML_CCA type.
type CommandAttributesList []CommandAttributes

// AlgorithmList is a slice of AlgorithmId values, and corresponds to the TPML_ALG type.
type AlgorithmList []AlgorithmId

// HandleList is a slice of Handle values, and corresponds to the TPML_HANDLE type.
type HandleList []Handle

// DigestList is a slice of Digest values, and corresponds to the TPML_DIGEST type.
type DigestList []Digest

// TaggedHashList is a slice of TaggedHash values, and corresponds to the TPML_DIGEST_VALUES type.
type TaggedHashList []TaggedHash

// PCRSelectionList is a slice of PCRSelection values, and corresponds to the TPML_PCR_SELECTION type.
type PCRSelectionList []PCRSelection

// WithMinSelectSize creates a copy of this list of selections with the minimum
// size of each selection in bytes set to the specified value. If this isn't
// used to change the default of zero, then 3 is assumed during marshalling
// which aligns with PC client TPM devices.
//
// Methods of TPMContext that accept a PCRSelectionList call this function
// already.
func (l PCRSelectionList) WithMinSelectSize(sz uint8) (out PCRSelectionList) {
	for _, s := range l {
		out = append(out, PCRSelection{Hash: s.Hash, Select: s.Select, SizeOfSelect: sz})
	}
	return out
}

// Equal indicates whether l and r contain the same PCR selections. Equal
// selections will marshal to the same bytes in the TPM wire format.
//
// This will panic if either selection list cannot be marshalled to the TPM
// wire format. Use mu.IsValid to check if the values can actually be
// serialized correctly.
//
// Deprecated: Use mu.DeepEqual instead.
func (l PCRSelectionList) Equal(r PCRSelectionList) bool {
	lb := mu.MustMarshalToBytes(l)
	rb := mu.MustMarshalToBytes(r)
	return bytes.Equal(lb, rb)
}

// Sort will sort the list of PCR selections in order of ascending algorithm
// ID. A new list of selections is returned.
//
// This will panic if the selection list cannot be marshalled to the TPM wire
// format. Use mu.IsValid to check if it can actually be serialized correctly.
func (l PCRSelectionList) Sort() (out PCRSelectionList) {
	mu.MustCopyValue(&out, l)
	for i, s := range l {
		out[i].SizeOfSelect = s.SizeOfSelect
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return
}

// Merge will merge the PCR selections specified by l and r together and
// return a new set of PCR selections which contains a combination of both.
// For each PCR found in r that isn't found in l, it will be added to the
// first occurence of the corresponding PCR bank found in l if that exists,
// or otherwise a selection for that PCR bank will be appended to the result.
//
// This will panic if either selection list cannot be marshalled to the TPM
// wire format. Use mu.IsValid to check if the values can actually be
// serialized correctly.
func (l PCRSelectionList) Merge(r PCRSelectionList) (out PCRSelectionList) {
	mu.MustCopyValue(&out, l)
	for i, s := range l {
		out[i].SizeOfSelect = s.SizeOfSelect
	}

	for _, sr := range r {
		rbmp, err := sr.Select.ToBitmap(math.MaxUint8)
		if err != nil {
			panic(err)
		}

		dsti := -1
		var dstbmp PCRSelectBitmap

		for i, sl := range out {
			if sl.Hash != sr.Hash {
				continue
			}

			lbmp, err := sl.Select.ToBitmap(math.MaxUint8)
			if err != nil {
				panic(err)
			}

			if dsti == -1 {
				dsti = i
				dstbmp = lbmp
			}

			for j := 0; j < math.MaxUint8; j++ {
				rbmp[j] &^= lbmp[j]
			}
		}

		if dsti > -1 {
			for j := 0; j < math.MaxUint8; j++ {
				dstbmp[j] |= rbmp[j]
			}
			out[dsti].Select = dstbmp.ToPCRs()
		} else {
			var sr2 PCRSelection
			mu.MustCopyValue(&sr2, sr)
			sr2.SizeOfSelect = sr.SizeOfSelect
			out = append(out, sr2)
		}
	}

	return out
}

// Remove will remove the PCR selections in r from the PCR selections in l,
// and return a new set of selections.
//
// This will panic if either selection list cannot be marshalled to the TPM
// wire format. Use mu.IsValid to check if the values can actually be
// serialized correctly.
func (l PCRSelectionList) Remove(r PCRSelectionList) (out PCRSelectionList) {
	mu.MustCopyValue(&out, l)
	for i, s := range l {
		out[i].SizeOfSelect = s.SizeOfSelect
	}

	for _, sr := range r {
		rbmp, err := sr.Select.ToBitmap(math.MaxUint8)
		if err != nil {
			panic(err)
		}

		for i, sl := range out {
			if sl.Hash != sr.Hash {
				continue
			}

			lbmp, err := sl.Select.ToBitmap(math.MaxUint8)
			if err != nil {
				panic(err)
			}

			for j := 0; j < math.MaxUint8; j++ {
				lbmp[j] &^= rbmp[j]
			}

			out[i].Select = lbmp.ToPCRs()
		}
	}

	for i, so := range out {
		if len(so.Select) > 0 {
			continue
		}
		if i < len(out)-1 {
			copy(out[i:], out[i+1:])
		}
		out = out[:len(out)-1]
	}

	return out
}

// IsEmpty returns true if the list of PCR selections selects no PCRs.
func (l PCRSelectionList) IsEmpty() bool {
	for _, s := range l {
		if len(s.Select) > 0 {
			return false
		}
	}
	return true
}

// AlgorithmPropertyList is a slice of AlgorithmProperty values, and corresponds to
// the TPML_ALG_PROPERTY type.
type AlgorithmPropertyList []AlgorithmProperty

// TaggedTPMPropertyList is a slice of TaggedProperty values, and corresponds to the
// TPML_TAGGED_TPM_PROPERTY type.
type TaggedTPMPropertyList []TaggedProperty

// TaggedPCRPropertyList is a slice of TaggedPCRSelect values, and corresponds to the
// TPML_TAGGED_PCR_PROPERTY type.
type TaggedPCRPropertyList []TaggedPCRSelect

// ECCCurveList is a slice of ECCCurve values, and corresponds to the TPML_ECC_CURVE type.
type ECCCurveList []ECCCurve

// TaggedPolicyList is a slice of TaggedPolicy values, and corresponds to the
// TPML_TAGGED_POLICY type.
type TaggedPolicyList []TaggedPolicy

// 10.10) Capabilities Structures

// Capabilities is a union type that corresponds to the TPMU_CAPABILITIES type. The
// selector type is Capability. Mapping of selector values to fields is as follows:
//   - CapabilityAlgs: Algorithms
//   - CapabilityHandles: Handles
//   - CapabilityCommands: Command
//   - CapabilityPPCommands: PPCommands
//   - CapabilityAuditCommands: AuditCommands
//   - CapabilityPCRs: AssignedPCR
//   - CapabilityTPMProperties: TPMProperties
//   - CapabilityPCRProperties: PCRProperties
//   - CapabilityECCCurves: ECCCurves
//   - CapabilityAuthPolicies: AuthPolicies
type CapabilitiesU struct {
	Algorithms    AlgorithmPropertyList
	Handles       HandleList
	Command       CommandAttributesList
	PPCommands    CommandCodeList
	AuditCommands CommandCodeList
	AssignedPCR   PCRSelectionList
	TPMProperties TaggedTPMPropertyList
	PCRProperties TaggedPCRPropertyList
	ECCCurves     ECCCurveList
	AuthPolicies  TaggedPolicyList
}

func (c *CapabilitiesU) Select(selector reflect.Value) interface{} {
	switch selector.Interface().(Capability) {
	case CapabilityAlgs:
		return &c.Algorithms
	case CapabilityHandles:
		return &c.Handles
	case CapabilityCommands:
		return &c.Command
	case CapabilityPPCommands:
		return &c.PPCommands
	case CapabilityAuditCommands:
		return &c.AuditCommands
	case CapabilityPCRs:
		return &c.AssignedPCR
	case CapabilityTPMProperties:
		return &c.TPMProperties
	case CapabilityPCRProperties:
		return &c.PCRProperties
	case CapabilityECCCurves:
		return &c.ECCCurves
	case CapabilityAuthPolicies:
		return &c.AuthPolicies
	default:
		return nil
	}
}

// CapabilityData corresponds to the TPMS_CAPABILITY_DATA type, and is returned by
// TPMContext.GetCapability.
type CapabilityData struct {
	Capability Capability     // Capability
	Data       *CapabilitiesU // Capability data
}

// 10.11 Clock/Counter Structures

// ClockInfo corresponds to the TPMS_CLOCK_INFO type.
type ClockInfo struct {
	Clock      uint64 // Time value in milliseconds that increments whilst the TPM is powered
	ResetCount uint32 // Number of TPM resets since the TPM was last cleared

	// RestartCount is the number of TPM restarts or resumes since the last TPM reset or the last time the TPM was cleared.
	RestartCount uint32

	// Safe indicates the the value reported by Clock is guaranteed to be unique for the current owner.
	Safe bool
}

// TimeInfo corresponds to the TPMS_TIME_INFO type.
type TimeInfo struct {
	Time      uint64    // Time value in milliseconds since the last TPM startup
	ClockInfo ClockInfo // Clock information
}

// 10.12 Attestation Structures

// TimeAttestInfo corresponds to the TPMS_TIME_ATTEST_INFO type, and is returned by
// TPMContext.GetTime.
type TimeAttestInfo struct {
	Time            TimeInfo // Time information
	FirmwareVersion uint64   // TPM vendor specific value indicating the version of the firmware
}

// CertifyInfo corresponds to the TPMS_CERTIFY_INFO type, and is returned by TPMContext.Certify.
type CertifyInfo struct {
	Name          Name // Name of the certified object
	QualifiedName Name // Qualified name of the certified object
}

// QuoteInfo corresponds to the TPMS_QUOTE_INFO type, and is returned by TPMContext.Quote.
type QuoteInfo struct {
	PCRSelect PCRSelectionList // PCRs included in PCRDigest
	PCRDigest Digest           // Digest of the selected PCRs, using the hash algorithm of the signing key
}

// CommandAuditInfo corresponds to the TPMS_COMMAND_AUDIT_INFO type, and is returned by
// TPMContext.GetCommandAuditDigest.
type CommandAuditInfo struct {
	AuditCounter  uint64      // Monotonic audit counter
	DigestAlg     AlgorithmId // Hash algorithm used for the command audit
	AuditDigest   Digest      // Current value of the audit digest
	CommandDigest Digest      // Digest of command codes being audited, using DigestAlg
}

// SessionAuditInfo corresponds to the TPMS_SESSION_AUDIT_INFO type, and is returned by
// TPMContext.GetSessionAuditDigest.
type SessionAuditInfo struct {
	// ExclusiveSession indicates the current exclusive status of the session. It is true if all of the commands recorded in
	// SessionDigest were executed without any intervening commands that did not use
	// the audit session.
	ExclusiveSession bool
	SessionDigest    Digest // Current value of the session audit digest
}

// CreationInfo corresponds to the TPMS_CREATION_INFO type, and is returned by TPMContext.CertifyCreation.
type CreationInfo struct {
	ObjectName   Name // Name of the object
	CreationHash Digest
}

// NVCertifyInfo corresponds to the TPMS_NV_CERTIFY_INFO type, and is returned by TPMContext.NVCertify.
type NVCertifyInfo struct {
	IndexName  Name        // Name of the NV index
	Offset     uint16      // Offset parameter of TPMContext.NVCertify
	NVContents MaxNVBuffer // Contents of the NV index
}

// AttestU is a union type that corresponds to the TPMU_ATTEST type. The selector type is StructTag.
// Mapping of selector values to fields is as follows:
//   - TagAttestNV: NV
//   - TagAttestCommandAudit: CommandAudit
//   - TagAttestSessionAudit: SessionAudit
//   - TagAttestCertify: Certify
//   - TagAttestQuote: Quote
//   - TagAttestTime: Time
//   - TagAttestCreation: Creation
type AttestU struct {
	Certify      *CertifyInfo
	Creation     *CreationInfo
	Quote        *QuoteInfo
	CommandAudit *CommandAuditInfo
	SessionAudit *SessionAuditInfo
	Time         *TimeAttestInfo
	NV           *NVCertifyInfo
}

func (a *AttestU) Select(selector reflect.Value) interface{} {
	switch selector.Interface().(StructTag) {
	case TagAttestNV:
		return &a.NV
	case TagAttestCommandAudit:
		return &a.CommandAudit
	case TagAttestSessionAudit:
		return &a.SessionAudit
	case TagAttestCertify:
		return &a.Certify
	case TagAttestQuote:
		return &a.Quote
	case TagAttestTime:
		return &a.Time
	case TagAttestCreation:
		return &a.Creation
	default:
		return nil
	}
}

// Attest corresponds to the TPMS_ATTEST type, and is returned by the attestation commands. The
// signature of the attestation is over this structure.
type Attest struct {
	Magic           TPMGenerated // Always TPMGeneratedValue
	Type            StructTag    // Type of the attestation structure
	QualifiedSigner Name         // Qualified name of the signing key
	ExtraData       Data         // External information provided by the caller
	ClockInfo       ClockInfo    // Clock information
	FirmwareVersion uint64       // TPM vendor specific value indicating the version of the firmware
	Attested        *AttestU     `tpm2:"selector:Type"` // Type specific attestation data
}

// 10.13) Authorization Structures

// AuthCommand corresppnds to the TPMS_AUTH_COMMAND type, and represents an authorization
// for a command.
type AuthCommand struct {
	SessionHandle     Handle
	Nonce             Nonce
	SessionAttributes SessionAttributes
	HMAC              Auth
}

// AuthResponse corresponds to the TPMS_AUTH_RESPONSE type, and represents an authorization
// response for a command.
type AuthResponse struct {
	Nonce             Nonce
	SessionAttributes SessionAttributes
	HMAC              Auth
}
