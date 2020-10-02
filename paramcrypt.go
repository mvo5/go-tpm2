// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

import (
	"crypto/aes"
	"encoding/binary"
	"fmt"

	"github.com/canonical/go-tpm2/internal"
	"github.com/canonical/go-tpm2/mu"
)

func isParamEncryptable(param interface{}) bool {
	return mu.DetermineTPMKind(param) == mu.TPMKindSized
}

func (s *sessionParam) computeSessionValue() []byte {
	var key []byte
	key = append(key, s.session.scData().SessionKey...)
	if s.associatedContext != nil {
		key = append(key, s.associatedContext.(resourceContextPrivate).authValue()...)
	}
	return key
}

func (p *sessionParams) findDecryptSession() (*sessionParam, int) {
	return p.findSessionWithAttr(AttrCommandEncrypt)
}

func (p *sessionParams) findEncryptSession() (*sessionParam, int) {
	return p.findSessionWithAttr(AttrResponseEncrypt)
}

func (p *sessionParams) hasDecryptSession() bool {
	s, _ := p.findDecryptSession()
	return s != nil
}

func (p *sessionParams) computeEncryptNonce() {
	s, i := p.findEncryptSession()
	if s == nil || i == 0 || !p.sessions[0].isAuth() {
		return
	}
	ds, di := p.findDecryptSession()
	if ds != nil && di == i {
		return
	}

	p.sessions[0].encryptNonce = s.session.scData().NonceTPM
}

func (p *sessionParams) encryptCommandParameter(cpBytes []byte) error {
	s, i := p.findDecryptSession()
	if s == nil {
		return nil
	}

	scData := s.session.scData()
	if !scData.HashAlg.Supported() {
		return fmt.Errorf("invalid digest algorithm: %v", scData.HashAlg)
	}

	sessionValue := s.computeSessionValue()

	size := binary.BigEndian.Uint16(cpBytes)
	data := cpBytes[2 : size+2]

	symmetric := scData.Symmetric

	switch symmetric.Algorithm {
	case SymAlgorithmAES:
		k := internal.KDFa(scData.HashAlg.GetHash(), sessionValue, []byte("CFB"), scData.NonceCaller, scData.NonceTPM,
			int(symmetric.KeyBits.Sym())+(aes.BlockSize*8))
		offset := (symmetric.KeyBits.Sym() + 7) / 8
		symKey := k[0:offset]
		iv := k[offset:]
		if err := internal.EncryptSymmetricAES(symKey, internal.SymmetricMode(symmetric.Mode.Sym()), data, iv); err != nil {
			return fmt.Errorf("AES encryption failed: %v", err)
		}
	case SymAlgorithmXOR:
		internal.XORObfuscation(scData.HashAlg.GetHash(), sessionValue, scData.NonceCaller, scData.NonceTPM, data)
	default:
		return fmt.Errorf("unknown symmetric algorithm: %v", symmetric.Algorithm)
	}

	if i > 0 && p.sessions[0].isAuth() {
		p.sessions[0].decryptNonce = scData.NonceTPM
	}

	return nil
}

func (p *sessionParams) decryptResponseParameter(rpBytes []byte) error {
	s, _ := p.findEncryptSession()
	if s == nil {
		return nil
	}

	scData := s.session.scData()
	if !scData.HashAlg.Supported() {
		return fmt.Errorf("invalid digest algorithm: %v", scData.HashAlg)
	}

	sessionValue := s.computeSessionValue()

	size := binary.BigEndian.Uint16(rpBytes)
	data := rpBytes[2 : size+2]

	symmetric := scData.Symmetric

	switch symmetric.Algorithm {
	case SymAlgorithmAES:
		k := internal.KDFa(scData.HashAlg.GetHash(), sessionValue, []byte("CFB"), scData.NonceTPM, scData.NonceCaller,
			int(symmetric.KeyBits.Sym())+(aes.BlockSize*8))
		offset := (symmetric.KeyBits.Sym() + 7) / 8
		symKey := k[0:offset]
		iv := k[offset:]
		if err := internal.DecryptSymmetricAES(symKey, internal.SymmetricMode(symmetric.Mode.Sym()), data, iv); err != nil {
			return fmt.Errorf("AES encryption failed: %v", err)
		}
	case SymAlgorithmXOR:
		internal.XORObfuscation(scData.HashAlg.GetHash(), sessionValue, scData.NonceTPM, scData.NonceCaller, data)
	default:
		return fmt.Errorf("unknown symmetric algorithm: %v", symmetric.Algorithm)
	}

	return nil
}
