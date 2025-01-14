// Copyright 2023 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package objectutil

import (
	"github.com/canonical/go-tpm2"
)

// Usage describes the usage of a key.
type Usage int

const (
	// UsageSign indicates that a key can be used for signing.
	UsageSign Usage = 1 << iota

	// UsageDecrypt indicates that a key can be used for decryption.
	UsageDecrypt

	// UsageEncrypt indicates that a key can be used for encryption.
	UsageEncrypt = UsageSign

	// UsageKeyAgreement indicates that a key can be used for key agreement.
	UsageKeyAgreement = UsageDecrypt
)

// PublicTemplateOption provides a way to customize the parameters of a public area or public
// template.
type PublicTemplateOption func(*tpm2.Public)

// WithNameAlg returns an option for the specified name algorithm.
func WithNameAlg(alg tpm2.HashAlgorithmId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		pub.NameAlg = alg
	}
}

// AuthMode represents an authorization mode for an object.
type AuthMode int

const (
	// AllowAuthValue indicates that an object's auth value can be used for authorization with a
	// passphrase or HMAC session, in addition to a policy session.
	AllowAuthValue AuthMode = iota + 1

	// RequirePolicy indicates that only a policy session can be used for authorization.
	RequirePolicy
)

// WithUserAuthMode returns an option that specifies the supplied mode should be used for
// authorization with the user role.
func WithUserAuthMode(mode AuthMode) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		switch mode {
		case AllowAuthValue:
			pub.Attrs |= tpm2.AttrUserWithAuth
		case RequirePolicy:
			pub.Attrs &^= tpm2.AttrUserWithAuth
		default:
			panic("invalid mode")
		}
	}
}

// WithAdminAuthMode returns an option that specifies the supplied mode should be used for
// authorization with the admin role.
func WithAdminAuthMode(mode AuthMode) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		switch mode {
		case AllowAuthValue:
			pub.Attrs &^= tpm2.AttrAdminWithPolicy
		case RequirePolicy:
			pub.Attrs |= tpm2.AttrAdminWithPolicy
		default:
			panic("invalid mode")
		}
	}
}

// WithDictionaryAttackProtection returns an option that enables DA protection for an object.
func WithDictionaryAttackProtection() PublicTemplateOption {
	return func(pub *tpm2.Public) {
		pub.Attrs &^= tpm2.AttrNoDA
	}
}

// WithoutDictionaryAttackProtection returns an option that disables DA protection for an object.
func WithoutDictionaryAttackProtection() PublicTemplateOption {
	return func(pub *tpm2.Public) {
		pub.Attrs |= tpm2.AttrNoDA
	}
}

// WithExternalSensitiveData returns an option that indicates the sensitive data for an object was
// or is to be generated outside of the TPM.
func WithExternalSensitiveData() PublicTemplateOption {
	return func(pub *tpm2.Public) {
		pub.Attrs &^= tpm2.AttrSensitiveDataOrigin
	}
}

// WithInternalSensitiveData returns an option that indicates the sensitive data for an object
// was or is to be generated by the TPM.
func WithInternalSensitiveData() PublicTemplateOption {
	return func(pub *tpm2.Public) {
		pub.Attrs |= tpm2.AttrSensitiveDataOrigin
	}
}

// ProtectionGroupMode describes the protection group that an object is created within.
type ProtectionGroupMode int

const (
	// NonDuplicable indicates that the protection group is not duplicable. This implies
	// tpm2.AttrFixedTPM and tpm2.AttrFixedParent are both set.
	NonDuplicable ProtectionGroupMode = iota + 1

	// Duplicable indicates that the protection group is duplicable. This implies that
	// tpm2.AttrFixedTPM is not set.
	Duplicable

	// DuplicableEncrypted indicates that the protection group is duplicable with encryption.
	// This implies that tpm2.AttrFixedTPM is not set and tpm2.AttrEncryptedDuplication is set.
	DuplicableEncrypted
)

// WithProtectionGroupMode returns an option for the specified protection group mode, which
// describes the hierarchy that an object is created within.
//
// If mode is [NonDuplicable], then [tpm2.AttrFixedTPM] will be set and
// [tpm2.AttrEncryptedDuplication] will be unset.
//
// If mode is [Duplicable], then both [tpm2.AttrFixedTPM] and [tpm2.AttrEncryptedDuplication] will
// be unset.
//
// If mode is [DuplicableEncrypted], then [tpm2.AttrFixedTPM] will be unset and
// [tpm2.AttrEncryptedDuplication] will be set.
//
// That this option always sets [tpm2.AttrFixedParent] attribute. To update this attribute and
// control whether an object can be duplicated directly, use [WithDuplicationMode] after using
// this.
func WithProtectionGroupMode(mode ProtectionGroupMode) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		switch mode {
		case NonDuplicable:
			pub.Attrs &^= tpm2.AttrEncryptedDuplication
			pub.Attrs |= (tpm2.AttrFixedTPM | tpm2.AttrFixedParent)
		case Duplicable:
			pub.Attrs &^= (tpm2.AttrFixedTPM | tpm2.AttrEncryptedDuplication)
			pub.Attrs |= tpm2.AttrFixedParent
		case DuplicableEncrypted:
			pub.Attrs &^= tpm2.AttrFixedTPM
			pub.Attrs |= tpm2.AttrFixedParent | tpm2.AttrEncryptedDuplication
		default:
			panic("invalid mode")
		}
	}
}

// DuplicationMode describes whether an object can be duplicated directly.
type DuplicationMode int

const (
	// FixedParent indicates that the object cannot be duplicated directory. This implies that
	// tpm2.AttrFixedParent is set.
	FixedParent DuplicationMode = iota + 1

	// DuplicationRoot indicates that the object is a duplication root. This implies that
	// tpm2.AttrFixedParent is not set.
	DuplicationRoot

	// DuplicationRootEncrypted indicates that the object is a duplication root and duplication
	// requires encryption. This implies that tpm2.AttrFixedParent is not set and
	// tpm2.AttrEncryptedDuplication is set.
	DuplicationRootEncrypted
)

// WithDuplicationMode returns an option for the specified duplication mode, which describes
// whether an object can be duplicated. This option expects [tpm2.AttrFixedParent] to be set, which
// is set when describing the protection mode of the hierarchy that the object is created within
// by using [WithProtectionGroupMode] before this.
//
// If mode is [FixedParent], no further changes are made to the object's attributes.
//
// If mode is [DuplicationRoot], this unsets both [tpm2.AttrFixedTPM] and [tpm2.AttrFixedParent], and
// doesn't change [tpm2.AttrEncryptedDuplication]. In this case, whether encrypted duplication is
// required will be determined by the protection group, which is inherited from the result of
// [WithProtectionGroupMode].
//
// If mode is [DuplicationRootEncrypted], this behaves like [DuplicationRoot] but also sets
// [tpm2.AttrEncryptedDuplication] so that duplication requires encryption. Note that this is only
// valid if the protection group the object is created within is not duplicable (the parent object
// has the [tpm2.AttrFixedTPM] attribute set) or the protection group is already duplicable with
// encryption (the parent object has the [tpm2.AttrFixedTPM] attribute unset and the
// [tpm2.AttrEncryptedDuplication] attribute set).
func WithDuplicationMode(mode DuplicationMode) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Attrs&tpm2.AttrFixedParent == 0 {
			panic("invalid hierarchy config - use WithProtectionGroupMode first")
		}

		switch mode {
		case FixedParent:
			// no changes
		case DuplicationRoot:
			pub.Attrs &^= (tpm2.AttrFixedTPM | tpm2.AttrFixedParent)
		case DuplicationRootEncrypted:
			if pub.Attrs&(tpm2.AttrFixedTPM|tpm2.AttrEncryptedDuplication) == 0 {
				panic("invalid mode for protection group")
			}
			pub.Attrs &^= (tpm2.AttrFixedTPM | tpm2.AttrFixedParent)
			pub.Attrs |= tpm2.AttrEncryptedDuplication
		default:
			panic("invalid mode")
		}
	}
}

// WithSymmetricScheme returns an option for the specified symmetric mode. This will panic for
// objects with the type [tpm2.ObjectTypeKeyedHash].
//
// Symmetric keys and asymmetric storage keys always have a symmetric scheme. Other keys never have
// a symmetric scheme. Only [tpm2.SymModeCFB] is valid for storage keys.
func WithSymmetricScheme(alg tpm2.SymObjectAlgorithmId, keyBits uint16, mode tpm2.SymModeId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		sym := tpm2.SymDefObject{
			Algorithm: alg,
			KeyBits:   &tpm2.SymKeyBitsU{Sym: keyBits},
			Mode:      &tpm2.SymModeU{Sym: mode}}

		switch pub.Type {
		case tpm2.ObjectTypeRSA:
			pub.Params.RSADetail.Symmetric = sym
		case tpm2.ObjectTypeECC:
			pub.Params.ECCDetail.Symmetric = sym
		case tpm2.ObjectTypeSymCipher:
			pub.Params.SymDetail.Sym = sym
		default:
			panic("invalid object type")
		}
	}
}

// WithSymmetricUnique returns an option for the specified public identity. This will panic for
// objects with a type other than [tpm2.ObjectTypeSymCipher].
//
// This is useful when creating templates for primary keys.
func WithSymmetricUnique(unique tpm2.Digest) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeSymCipher {
			panic("invalid object type")
		}

		pub.Unique = &tpm2.PublicIDU{Sym: make([]byte, len(unique))}
		copy(pub.Unique.Sym, unique)
	}
}

// WithRSAKeyBits returns an option for the specified RSA key size in bits. This will panic for
// objects with a type other than [tpm2.ObjectTypeRSA].
func WithRSAKeyBits(keyBits uint16) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeRSA {
			panic("invalid object type")
		}

		pub.Params.RSADetail.KeyBits = keyBits
	}
}

// WithRSAParams returns an option for the specified RSA key size in bits and the specified
// pbulic exponent. This will panic for objects with a type other than [tpm2.ObjectTypeRSA].
func WithRSAParams(keyBits uint16, exponent uint32) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeRSA {
			panic("invalid object type")
		}

		if exponent == tpm2.DefaultRSAExponent {
			exponent = 0
		}
		pub.Params.RSADetail.KeyBits = keyBits
		pub.Params.RSADetail.Exponent = exponent
	}
}

// WithRSAScheme returns an option for the specified RSA scheme. This will panic for objects with a
// type other than [tpm2.ObjectTypeRSA].
//
// Attestation keys always have a signing scheme. Storage keys never have a scheme set. Decrypt or
// signing keys may have an appropriate scheme set.
func WithRSAScheme(scheme tpm2.RSASchemeId, hashAlg tpm2.HashAlgorithmId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeRSA {
			panic("invalid object type")
		}

		s := tpm2.RSAScheme{
			Scheme:  scheme,
			Details: new(tpm2.AsymSchemeU)}
		switch scheme {
		case tpm2.RSASchemeRSASSA:
			s.Details.RSASSA = &tpm2.SigSchemeRSASSA{HashAlg: hashAlg}
		case tpm2.RSASchemeRSAES:
			s.Details.RSAES = new(tpm2.EncSchemeRSAES)
			if hashAlg != tpm2.HashAlgorithmNull {
				panic("invalid digest")
			}
		case tpm2.RSASchemeRSAPSS:
			s.Details.RSAPSS = &tpm2.SigSchemeRSAPSS{HashAlg: hashAlg}
		case tpm2.RSASchemeOAEP:
			s.Details.OAEP = &tpm2.EncSchemeOAEP{HashAlg: hashAlg}
		}

		pub.Params.RSADetail.Scheme = s
	}
}

// WithRSAUnique returns an option for the specified public identity. This will panic for
// objects with a type other than [tpm2.ObjectTypeRSA].
//
// This is useful when creating templates for primary keys.
func WithRSAUnique(unique tpm2.PublicKeyRSA) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeRSA {
			panic("invalid object type")
		}

		pub.Unique = &tpm2.PublicIDU{RSA: make([]byte, len(unique))}
		copy(pub.Unique.RSA, unique)
	}
}

// WithECCCurve returns an option for the specified elliptic curve. This will panic for objects with a
// type other than [tpm2.ObjectTypeECC].
func WithECCCurve(curve tpm2.ECCCurve) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeECC {
			panic("invalid object type")
		}

		pub.Params.ECCDetail.CurveID = curve
	}
}

// WithECCScheme returns an option for the specified ECC scheme. This will panic for objects with a
// type other than [tpm2.ObjectTypeECC].
//
// Attestation keys always have a signing scheme. Storage keys never have a scheme set. Key
// exchange or signing keys may have an appropriate scheme set.
func WithECCScheme(scheme tpm2.ECCSchemeId, hashAlg tpm2.HashAlgorithmId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeECC {
			panic("invalid object type")
		}

		s := tpm2.ECCScheme{
			Scheme:  scheme,
			Details: new(tpm2.AsymSchemeU)}
		switch scheme {
		case tpm2.ECCSchemeECDSA:
			s.Details.ECDSA = &tpm2.SigSchemeECDSA{HashAlg: hashAlg}
		case tpm2.ECCSchemeECDH:
			s.Details.ECDH = &tpm2.KeySchemeECDH{HashAlg: hashAlg}
		case tpm2.ECCSchemeECDAA:
			s.Details.ECDAA = &tpm2.SigSchemeECDAA{HashAlg: hashAlg}
		case tpm2.ECCSchemeSM2:
			s.Details.SM2 = &tpm2.SigSchemeSM2{HashAlg: hashAlg}
		case tpm2.ECCSchemeECSchnorr:
			s.Details.ECSchnorr = &tpm2.SigSchemeECSchnorr{HashAlg: hashAlg}
		case tpm2.ECCSchemeECMQV:
			s.Details.ECMQV = &tpm2.KeySchemeECMQV{HashAlg: hashAlg}
		}

		pub.Params.ECCDetail.Scheme = s
	}
}

// WithECCUnique returns an option for the specified public identity. This will panic for
// objects with a type other than [tpm2.ObjectTypeECC].
//
// This is useful when creating templates for primary keys.
func WithECCUnique(unique *tpm2.ECCPoint) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeECC {
			panic("invalid object type")
		}

		pub.Unique = &tpm2.PublicIDU{
			ECC: &tpm2.ECCPoint{
				X: make([]byte, len(unique.X)),
				Y: make([]byte, len(unique.Y))}}
		copy(pub.Unique.ECC.X, unique.X)
		copy(pub.Unique.ECC.Y, unique.Y)
	}
}

// WithHMACDigest returns an option for the specified HMAC digest algorithm. This will panic for
// objects with a type other than [tpm2.ObjectTypeKeyedHash] and a scheme other than
// [tpm2.KeyedHashSchemeHMAC].
func WithHMACDigest(alg tpm2.HashAlgorithmId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeKeyedHash || pub.Params.KeyedHashDetail.Scheme.Scheme != tpm2.KeyedHashSchemeHMAC {
			panic("invalid object type")
		}

		pub.Params.KeyedHashDetail.Scheme.Details.HMAC = &tpm2.SchemeHMAC{HashAlg: alg}
	}
}

// WithDerivationScheme returns an option for the specified derivation scheme. This will panic for
// objects with a type other than [tpm2.ObjectTypeKeyedHash], a scheme other than
// [tpm2.KeyedHashSchemeXOR] and objects that aren't parents. This option is intended for
// derivation parents.
func WithDerivationScheme(hashAlg tpm2.HashAlgorithmId, kdf tpm2.KDFAlgorithmId) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeKeyedHash || pub.Params.KeyedHashDetail.Scheme.Scheme != tpm2.KeyedHashSchemeXOR || pub.Attrs&(tpm2.AttrRestricted|tpm2.AttrDecrypt|tpm2.AttrSign) != (tpm2.AttrRestricted|tpm2.AttrDecrypt) {
			panic("invalid object type")
		}

		pub.Params.KeyedHashDetail.Scheme.Details.XOR = &tpm2.SchemeXOR{HashAlg: hashAlg, KDF: kdf}
	}
}

// WithKeyedHashUnique returns an option for the specified public identity. This will panic for
// objects with a type other than [tpm2.ObjectTypeKeyedHash].
//
// This is useful when creating templates for primary keys.
func WithKeyedHashUnique(unique tpm2.Digest) PublicTemplateOption {
	return func(pub *tpm2.Public) {
		if pub.Type != tpm2.ObjectTypeKeyedHash {
			panic("invalid object type")
		}

		pub.Unique = &tpm2.PublicIDU{KeyedHash: make([]byte, len(unique))}
		copy(pub.Unique.KeyedHash, unique)
	}
}

func applyPublicTemplateOptions(pub *tpm2.Public, options ...PublicTemplateOption) {
	for _, option := range options {
		option(pub)
	}
}

// NewRSAStorageKeyTemplate returns a template for a RSA storage key. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - AES-128-CFB for the symmetric scheme - customize with [WithSymmetricScheme].
//   - RSA key size of 2048 bits - customize with [WithRSAKeyBits].
func NewRSAStorageKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeRSA,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrDecrypt,
		Params: &tpm2.PublicParamsU{
			RSADetail: &tpm2.RSAParams{
				Symmetric: tpm2.SymDefObject{
					Algorithm: tpm2.SymObjectAlgorithmAES,
					KeyBits:   &tpm2.SymKeyBitsU{Sym: 128},
					Mode:      &tpm2.SymModeU{Sym: tpm2.SymModeCFB}},
				Scheme:   tpm2.RSAScheme{Scheme: tpm2.RSASchemeNull},
				KeyBits:  2048,
				Exponent: 0}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewRSAAttestationKeyTemplate returns a template for a RSA attestation key. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - RSA key size of 2048 bits - customize with [WithRSAKeyBits].
//   - RSA-PSS and SHA-256 for the RSA scheme - customize with [WithRSAScheme].
func NewRSAAttestationKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeRSA,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrSign,
		Params: &tpm2.PublicParamsU{
			RSADetail: &tpm2.RSAParams{
				Symmetric: tpm2.SymDefObject{Algorithm: tpm2.SymObjectAlgorithmNull},
				Scheme: tpm2.RSAScheme{
					Scheme: tpm2.RSASchemeRSAPSS,
					Details: &tpm2.AsymSchemeU{
						RSAPSS: &tpm2.SigSchemeRSAPSS{HashAlg: tpm2.HashAlgorithmSHA256}}},
				KeyBits:  2048,
				Exponent: 0}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewRSAKeyTemplate returns a template for a RSA key with the specicied usage. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - RSA key size of 2048 bits - customize with [WithRSAKeyBits].
//   - No RSA scheme - customize with [WithRSAScheme].
func NewRSAKeyTemplate(usage Usage, options ...PublicTemplateOption) *tpm2.Public {
	if usage == 0 {
		panic("invalid usage")
	}

	attrs := tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth
	if usage&UsageDecrypt != 0 {
		attrs |= tpm2.AttrDecrypt
	}
	if usage&UsageSign != 0 {
		attrs |= tpm2.AttrSign
	}

	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeRSA,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   attrs,
		Params: &tpm2.PublicParamsU{
			RSADetail: &tpm2.RSAParams{
				Symmetric: tpm2.SymDefObject{Algorithm: tpm2.SymObjectAlgorithmNull},
				Scheme:    tpm2.RSAScheme{Scheme: tpm2.RSASchemeNull},
				KeyBits:   2048,
				Exponent:  0}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewECCStorageKeyTemplate returns a template for a ECC storage key. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - AES-128-CFB for the symmetric scheme - customize with [WithSymmetricScheme].
//   - NIST P-256 for the curve - customize with [WithECCCurve].
func NewECCStorageKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeECC,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrDecrypt,
		Params: &tpm2.PublicParamsU{
			ECCDetail: &tpm2.ECCParams{
				Symmetric: tpm2.SymDefObject{
					Algorithm: tpm2.SymObjectAlgorithmAES,
					KeyBits:   &tpm2.SymKeyBitsU{Sym: 128},
					Mode:      &tpm2.SymModeU{Sym: tpm2.SymModeCFB}},
				Scheme:  tpm2.ECCScheme{Scheme: tpm2.ECCSchemeNull},
				CurveID: tpm2.ECCCurveNIST_P256,
				KDF:     tpm2.KDFScheme{Scheme: tpm2.KDFAlgorithmNull}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewECCAttestationKeyTemplate returns a template for a ECC attestation key. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - NIST P-256 for the curve - customize with [WithECCCurve].
//   - ECDSA and SHA-256 for the ECC scheme - customize with [WithECCScheme].
func NewECCAttestationKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeECC,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrSign,
		Params: &tpm2.PublicParamsU{
			ECCDetail: &tpm2.ECCParams{
				Symmetric: tpm2.SymDefObject{Algorithm: tpm2.SymObjectAlgorithmNull},
				Scheme: tpm2.ECCScheme{
					Scheme: tpm2.ECCSchemeECDSA,
					Details: &tpm2.AsymSchemeU{
						ECDSA: &tpm2.SigSchemeECDSA{HashAlg: tpm2.HashAlgorithmSHA256}}},
				CurveID: tpm2.ECCCurveNIST_P256,
				KDF:     tpm2.KDFScheme{Scheme: tpm2.KDFAlgorithmNull}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewECCKeyTemplate returns a template for a ECC key with the specicied usage. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - NIST-P256 for the curve - customize with [WithECCCurve].
//   - No ECC scheme - customize with [WithECCScheme].
func NewECCKeyTemplate(usage Usage, options ...PublicTemplateOption) *tpm2.Public {
	if usage == 0 {
		panic("invalid usage")
	}

	attrs := tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth
	if usage&UsageKeyAgreement != 0 {
		attrs |= tpm2.AttrDecrypt
	}
	if usage&UsageSign != 0 {
		attrs |= tpm2.AttrSign
	}

	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeECC,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   attrs,
		Params: &tpm2.PublicParamsU{
			ECCDetail: &tpm2.ECCParams{
				Symmetric: tpm2.SymDefObject{Algorithm: tpm2.SymObjectAlgorithmNull},
				Scheme:    tpm2.ECCScheme{Scheme: tpm2.ECCSchemeNull},
				CurveID:   tpm2.ECCCurveNIST_P256,
				KDF:       tpm2.KDFScheme{Scheme: tpm2.KDFAlgorithmNull}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewSymmetricStorageKeyTemplate returns a template for a symmetric storage key. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Sensitive data generated by the TPM - customize with [WithInternalSensitiveData] and
//     [WithExternalSensitiveData].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - AES-128-CFB for the symmetric scheme - customize with [WithSymmetricScheme].
func NewSymmetricStorageKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeSymCipher,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrDecrypt,
		Params: &tpm2.PublicParamsU{
			SymDetail: &tpm2.SymCipherParams{
				Sym: tpm2.SymDefObject{
					Algorithm: tpm2.SymObjectAlgorithmAES,
					KeyBits:   &tpm2.SymKeyBitsU{Sym: 128},
					Mode:      &tpm2.SymModeU{Sym: tpm2.SymModeCFB}}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewSymmetricKeyTemplate returns a template for a symmetric key with the specicied usage. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Sensitive data generated by the TPM - customize with [WithInternalSensitiveData] and
//     [WithExternalSensitiveData].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - AES-128-CFB for the symmetric scheme - customize with [WithSymmetricScheme].
func NewSymmetricKeyTemplate(usage Usage, options ...PublicTemplateOption) *tpm2.Public {
	if usage == 0 {
		panic("invalid usage")
	}

	attrs := tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth
	if usage&UsageDecrypt != 0 {
		attrs |= tpm2.AttrDecrypt
	}
	if usage&UsageEncrypt != 0 {
		attrs |= tpm2.AttrSign
	}
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeSymCipher,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   attrs,
		Params: &tpm2.PublicParamsU{
			SymDetail: &tpm2.SymCipherParams{
				Sym: tpm2.SymDefObject{
					Algorithm: tpm2.SymObjectAlgorithmAES,
					KeyBits:   &tpm2.SymKeyBitsU{Sym: 128},
					Mode:      &tpm2.SymModeU{Sym: tpm2.SymModeCFB}}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewHMACKeyTemplate returns a template for a HMAC key. The template can be customized by
// supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Sensitive data generated by the TPM - customize with [WithInternalSensitiveData] and
//     [WithExternalSensitiveData].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - SHA-256 for the HMAC digest algorithm - customize with [WithHMACDigest].
func NewHMACKeyTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeKeyedHash,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrSign,
		Params: &tpm2.PublicParamsU{
			KeyedHashDetail: &tpm2.KeyedHashParams{
				Scheme: tpm2.KeyedHashScheme{
					Scheme: tpm2.KeyedHashSchemeHMAC,
					Details: &tpm2.SchemeKeyedHashU{
						HMAC: &tpm2.SchemeHMAC{HashAlg: tpm2.HashAlgorithmSHA256}}}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewDerivationParentTemplate returns a template for a derivation parent. The template can be
// customized by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Sensitive data generated by the TPM - customize with [WithInternalSensitiveData] and
//     [WithExternalSensitiveData].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
//   - SHA-256 and SP800-108 KDF for the derivation scheme - customize with [WithDerivationScheme].
func NewDerivationParentTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeKeyedHash,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrSensitiveDataOrigin | tpm2.AttrUserWithAuth | tpm2.AttrRestricted | tpm2.AttrDecrypt,
		Params: &tpm2.PublicParamsU{
			KeyedHashDetail: &tpm2.KeyedHashParams{
				Scheme: tpm2.KeyedHashScheme{
					Scheme: tpm2.KeyedHashSchemeXOR,
					Details: &tpm2.SchemeKeyedHashU{
						XOR: &tpm2.SchemeXOR{
							HashAlg: tpm2.HashAlgorithmSHA256,
							KDF:     tpm2.KDFAlgorithmKDF1_SP800_108}}}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}

// NewSealedObjectTemplate returns a template for a sealed object. The template can be customized
// by supplying additional options.
//
// Without any options, the template will have the following properties:
//   - SHA-256 for the name algorithm - customize with [WithNameAlg].
//   - Authorization with the object's auth value is permitted for both the user and admin roles -
//     customize with [WithUserAuthMode] and [WithAdminAuthMode].
//   - DA protected - customize with [WithDictionaryAttackProtection] and
//     [WithoutDictionaryAttackProtection].
//   - Not duplicable - customize with [WithProtectionGroupMode] and [WithDuplicationMode].
func NewSealedObjectTemplate(options ...PublicTemplateOption) *tpm2.Public {
	template := &tpm2.Public{
		Type:    tpm2.ObjectTypeKeyedHash,
		NameAlg: tpm2.HashAlgorithmSHA256,
		Attrs:   tpm2.AttrFixedTPM | tpm2.AttrFixedParent | tpm2.AttrUserWithAuth,
		Params: &tpm2.PublicParamsU{
			KeyedHashDetail: &tpm2.KeyedHashParams{
				Scheme: tpm2.KeyedHashScheme{Scheme: tpm2.KeyedHashSchemeNull}}}}
	applyPublicTemplateOptions(template, options...)
	return template
}
