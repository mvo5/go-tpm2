// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"
)

func TestPolicySigned(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	keyPublic := Public{
		Type:    ObjectTypeRSA,
		NameAlg: HashAlgorithmSHA256,
		Attrs:   AttrSensitiveDataOrigin | AttrUserWithAuth | AttrSign,
		Params: PublicParamsU{
			&RSAParams{
				Symmetric: SymDefObject{Algorithm: SymObjectAlgorithmNull},
				Scheme:    RSAScheme{Scheme: RSASchemeNull},
				KeyBits:   2048,
				Exponent:  uint32(key.PublicKey.E)}},
		Unique: PublicIDU{Digest(key.PublicKey.N.Bytes())}}
	keyContext, keyName, err := tpm.LoadExternal(nil, &keyPublic, HandleOwner)
	if err != nil {
		t.Fatalf("LoadExternal failed: %v", err)
	}
	defer flushContext(t, tpm, keyContext)

	testHash := make([]byte, 32)
	rand.Read(testHash)

	for _, data := range []struct {
		desc            string
		includeNonceTPM bool
		expiration      int32
		cpHashA         Digest
		policyRef       Nonce
	}{
		{
			desc: "Basic",
		},
		{
			desc:            "WithNonceTPM",
			includeNonceTPM: true,
		},
		{
			desc:      "WithPolicyRef",
			policyRef: []byte("foo"),
		},
		{
			desc:       "WithNegativeExpiration",
			expiration: -200,
		},
		{
			desc:       "WithExpiration",
			expiration: 100,
		},
		{
			desc:    "WithCpHash",
			cpHashA: testHash,
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			h := sha256.New()
			if data.includeNonceTPM {
				h.Write(sessionContext.NonceTPM())
			}
			binary.Write(h, binary.BigEndian, data.expiration)
			h.Write(data.cpHashA)
			h.Write(data.policyRef)

			aHash := h.Sum(nil)

			s, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, aHash, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			if err != nil {
				t.Fatalf("Signing failed: %v", err)
			}

			signature := Signature{
				SigAlg:    SigSchemeAlgRSAPSS,
				Signature: SignatureU{Data: &SignatureRSAPSS{Hash: HashAlgorithmSHA256, Sig: PublicKeyRSA(s)}}}

			timeout, policyTicket, err :=
				tpm.PolicySigned(keyContext, sessionContext, data.includeNonceTPM, data.cpHashA, data.policyRef, data.expiration, &signature)
			if err != nil {
				t.Fatalf("PolicySigned failed: %v", err)
			}

			if policyTicket == nil {
				t.Fatalf("Expected a policyTicket")
			}
			if policyTicket.Tag != TagAuthSigned {
				t.Errorf("Unexpected tag: %v", policyTicket.Tag)
			}

			if data.expiration >= 0 {
				if len(timeout) != 0 {
					t.Errorf("Expected an empty timeout")
				}
				if policyTicket.Hierarchy != HandleNull {
					t.Errorf("Unexpected hierarchy: 0x%08x", policyTicket.Hierarchy)
				}
			} else {
				if len(timeout) == 0 {
					t.Errorf("Expected a non zero-length timeout")
				}
				if policyTicket.Hierarchy != HandleOwner {
					t.Errorf("Unexpected hierarchy: 0x%08x", policyTicket.Hierarchy)
				}
			}

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicySigned(keyName, data.policyRef)

			policyDigest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(trial.GetDigest(), policyDigest) {
				t.Errorf("Unexpected digest")
			}
		})
	}
}

func TestPolicySecret(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	primary := createRSASrkForTesting(t, tpm, Auth(testAuth))
	defer flushContext(t, tpm, primary)

	run := func(t *testing.T, cpHashA []byte, policyRef Nonce, expiration int32, useSession func(SessionContext), auth interface{}) {
		sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
		if err != nil {
			t.Fatalf("StartAuthSession failed: %v", err)
		}
		defer flushContext(t, tpm, sessionContext)

		timeout, policyTicket, err := tpm.PolicySecret(primary, sessionContext, cpHashA, policyRef, expiration, auth)
		if err != nil {
			t.Fatalf("PolicySecret failed: %v", err)
		}

		if policyTicket == nil {
			t.Fatalf("Expected a policyTicket")
		}
		if policyTicket.Tag != TagAuthSecret {
			t.Errorf("Unexpected tag: %v", policyTicket.Tag)
		}

		if expiration >= 0 {
			if len(timeout) != 0 {
				t.Errorf("Expected an empty timeout")
			}
			if policyTicket.Hierarchy != HandleNull {
				t.Errorf("Unexpected hierarchy: 0x%08x", policyTicket.Hierarchy)
			}
		} else {
			if len(timeout) == 0 {
				t.Errorf("Expected a non zero-length timeout")
			}
			if policyTicket.Hierarchy != HandleOwner {
				t.Errorf("Unexpected hierarchy: 0x%08x", policyTicket.Hierarchy)
			}
		}

		policyDigest, err := tpm.PolicyGetDigest(sessionContext)
		if err != nil {
			t.Fatalf("PolicyGetDigest failed: %v", err)
		}

		trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
		trial.PolicySecret(primary.Name(), policyRef)

		if !bytes.Equal(trial.GetDigest(), policyDigest) {
			t.Errorf("Unexpected digest")
		}

		if useSession != nil {
			useSession(sessionContext)
		}
	}

	t.Run("UsePassword", func(t *testing.T) {
		run(t, nil, nil, 0, nil, testAuth)
	})
	t.Run("UseSession", func(t *testing.T) {
		sessionContext, err := tpm.StartAuthSession(nil, primary, SessionTypeHMAC, nil, HashAlgorithmSHA256)
		if err != nil {
			t.Fatalf("StartAuthSession failed: %v", err)
		}
		defer verifyContextFlushed(t, tpm, sessionContext)
		run(t, nil, nil, 0, nil, &Session{Context: sessionContext, AuthValue: testAuth})
	})
	t.Run("WithPolicyRef", func(t *testing.T) {
		run(t, nil, []byte("foo"), 0, nil, testAuth)
	})
	t.Run("WithNegativeExpiration", func(t *testing.T) {
		run(t, nil, nil, -100, nil, testAuth)
	})
	t.Run("WithExpiration", func(t *testing.T) {
		trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
		trial.PolicySecret(primary.Name(), nil)

		secret := []byte("secret data")
		template := Public{
			Type:       ObjectTypeKeyedHash,
			NameAlg:    HashAlgorithmSHA256,
			Attrs:      AttrFixedTPM | AttrFixedParent,
			AuthPolicy: trial.GetDigest(),
			Params:     PublicParamsU{&KeyedHashParams{Scheme: KeyedHashScheme{Scheme: KeyedHashSchemeNull}}}}
		sensitive := SensitiveCreate{Data: secret}

		outPrivate, outPublic, _, _, _, err := tpm.Create(primary, &sensitive, &template, nil, nil, testAuth)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		objectContext, _, err := tpm.Load(primary, outPrivate, outPublic, testAuth)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		defer flushContext(t, tpm, objectContext)

		useSession := func(sessionContext SessionContext) {
			time.Sleep(2 * time.Second)
			_, err := tpm.Unseal(objectContext, &Session{Context: sessionContext, Attrs: AttrContinueSession})
			if err == nil {
				t.Fatalf("Unseal should have failed")
			}
			if e, ok := err.(*TPMSessionError); !ok || e.Code() != ErrorExpired {
				t.Errorf("Unexpected error: %v", err)
			}
		}

		run(t, nil, nil, 1, useSession, testAuth)
	})
	t.Run("WithCpHash", func(t *testing.T) {
		trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
		trial.PolicySecret(primary.Name(), nil)

		secret1 := []byte("secret data1")
		secret2 := []byte("secret data2")
		template := Public{
			Type:       ObjectTypeKeyedHash,
			NameAlg:    HashAlgorithmSHA256,
			Attrs:      AttrFixedTPM | AttrFixedParent,
			AuthPolicy: trial.GetDigest(),
			Params:     PublicParamsU{&KeyedHashParams{Scheme: KeyedHashScheme{Scheme: KeyedHashSchemeNull}}}}
		sensitive1 := SensitiveCreate{Data: secret1}
		sensitive2 := SensitiveCreate{Data: secret2}

		outPrivate, outPublic, _, _, _, err := tpm.Create(primary, &sensitive1, &template, nil, nil, testAuth)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		objectContext1, _, err := tpm.Load(primary, outPrivate, outPublic, testAuth)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		defer flushContext(t, tpm, objectContext1)

		outPrivate, outPublic, _, _, _, err = tpm.Create(primary, &sensitive2, &template, nil, nil, testAuth)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		objectContext2, _, err := tpm.Load(primary, outPrivate, outPublic, testAuth)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		defer flushContext(t, tpm, objectContext2)

		cpHash, err := ComputeCpHash(HashAlgorithmSHA256, CommandUnseal, objectContext2)
		if err != nil {
			t.Fatalf("ComputeCpHash failed: %v", err)
		}

		useSession := func(sessionContext SessionContext) {
			_, err := tpm.Unseal(objectContext1, &Session{Context: sessionContext, Attrs: AttrContinueSession})
			if err == nil {
				t.Fatalf("Unseal should have failed")
			}
			if e, ok := err.(*TPMSessionError); !ok || e.Code() != ErrorPolicyFail {
				t.Errorf("Unexpected error: %v", err)
			}
			_, err = tpm.Unseal(objectContext2, &Session{Context: sessionContext, Attrs: AttrContinueSession})
			if err != nil {
				t.Errorf("Unseal failed: %v", err)
			}
		}

		run(t, cpHash, nil, 0, useSession, testAuth)
	})
}

func TestPolicyTicketFromSecret(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	primary := createRSASrkForTesting(t, tpm, Auth(testAuth))
	defer flushContext(t, tpm, primary)

	testHash := make([]byte, 32)
	rand.Read(testHash)

	for _, data := range []struct {
		desc      string
		cpHashA   Digest
		policyRef Nonce
	}{
		{
			desc: "Basic",
		},
		{
			desc:    "WithCpHash",
			cpHashA: testHash,
		},
		{
			desc:      "WithPolicyRef",
			policyRef: []byte("5678"),
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			sessionContext1, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext1)

			timeout, ticket, err := tpm.PolicySecret(primary, sessionContext1, data.cpHashA, data.policyRef, -60, testAuth)
			if err != nil {
				t.Fatalf("PolicySecret failed: %v", err)
			}

			sessionContext2, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext2)

			if err := tpm.PolicyTicket(sessionContext2, timeout, data.cpHashA, data.policyRef, primary.Name(), ticket); err != nil {
				t.Errorf("PolicyTicket failed: %v", err)
			}

			digest1, err := tpm.PolicyGetDigest(sessionContext1)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			digest2, err := tpm.PolicyGetDigest(sessionContext2)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest1, digest2) {
				t.Errorf("Unexpected digest")
			}
		})
	}
}

func TestPolicyTicketFromSigned(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	primary := createRSASrkForTesting(t, tpm, nil)
	defer flushContext(t, tpm, primary)

	key := createAndLoadRSAPSSKeyForTesting(t, tpm, primary)
	defer flushContext(t, tpm, key)

	testHash := make([]byte, 32)
	rand.Read(testHash)

	for _, data := range []struct {
		desc      string
		cpHashA   Digest
		policyRef Nonce
	}{
		{
			desc: "Basic",
		},
		{
			desc:    "WithCpHash",
			cpHashA: testHash,
		},
		{
			desc:      "WithPolicyRef",
			policyRef: []byte("5678"),
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			sessionContext1, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext1)

			h := sha256.New()
			h.Write(sessionContext1.NonceTPM())
			binary.Write(h, binary.BigEndian, int32(-60))
			h.Write(data.cpHashA)
			h.Write(data.policyRef)

			aHash := h.Sum(nil)

			signature, err := tpm.Sign(key, aHash, nil, nil, nil)
			if err != nil {
				t.Fatalf("Sign failed: %v", err)
			}

			timeout, ticket, err := tpm.PolicySigned(key, sessionContext1, true, data.cpHashA, data.policyRef, -60, signature)
			if err != nil {
				t.Fatalf("PolicySigned failed: %v", err)
			}

			sessionContext2, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext2)

			if err := tpm.PolicyTicket(sessionContext2, timeout, data.cpHashA, data.policyRef, key.Name(), ticket); err != nil {
				t.Errorf("PolicyTicket failed: %v", err)
			}

			digest1, err := tpm.PolicyGetDigest(sessionContext1)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			digest2, err := tpm.PolicyGetDigest(sessionContext2)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest1, digest2) {
				t.Errorf("Unexpected digest")
			}
		})
	}
}

func TestPolicyOR(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
	trial.PolicyCommandCode(CommandNVChangeAuth)
	digest := trial.GetDigest()

	digestList := []Digest{digest}
	for i := 0; i < 4; i++ {
		digest := make(Digest, sha256.Size)
		if _, err := rand.Read(digest); err != nil {
			t.Fatalf("Failed to get random data: %v", err)
		}
		digestList = append(digestList, digest)
	}

	trial.PolicyOR(digestList)

	sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
	if err != nil {
		t.Fatalf("StartAuthSession failed: %v", err)
	}
	defer flushContext(t, tpm, sessionContext)

	if err := tpm.PolicyCommandCode(sessionContext, CommandNVChangeAuth); err != nil {
		t.Fatalf("PolicyCommandCode failed: %v", err)
	}
	if err := tpm.PolicyOR(sessionContext, digestList); err != nil {
		t.Fatalf("PolicyOR failed: %v", err)
	}

	policyDigest, err := tpm.PolicyGetDigest(sessionContext)
	if err != nil {
		t.Fatalf("PolicyGetDigest failed: %v", err)
	}

	if !bytes.Equal(policyDigest, trial.GetDigest()) {
		t.Errorf("Unexpected policy digest")
	}
}

func TestPolicyPCR(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	for _, data := range []struct {
		index int
		data  []byte
	}{
		{
			index: 7,
			data:  []byte("foo"),
		},
		{
			index: 8,
			data:  []byte("bar"),
		},
		{
			index: 9,
			data:  []byte("1234"),
		},
	} {
		_, err := tpm.PCREvent(tpm.PCRHandleContext(data.index), data.data, nil)
		if err != nil {
			t.Fatalf("PCREvent failed: %v", err)
		}
	}

	for _, data := range []struct {
		desc   string
		digest Digest
		pcrs   PCRSelectionList
	}{
		{
			desc: "SinglePCRSingleBank",
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{7}}},
		},
		{
			desc: "SinglePCRMultipleBank",
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{8}},
				PCRSelection{Hash: HashAlgorithmSHA1, Select: PCRSelectionData{8}}},
		},
		{
			desc: "SinglePCRMultipleBank2",
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA1, Select: PCRSelectionData{8}},
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{8}}},
		},
		{
			desc: "MultiplePCRSingleBank",
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{7, 8, 9}}},
		},
		{
			desc: "MultiplePCRMultipleBank",
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{7, 8, 9}},
				PCRSelection{Hash: HashAlgorithmSHA1, Select: PCRSelectionData{7, 8, 9}}},
		},
		{
			desc: "WithDigest",
			digest: computePCRDigestFromTPM(t, tpm, HashAlgorithmSHA256, PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{8}},
				PCRSelection{Hash: HashAlgorithmSHA1, Select: PCRSelectionData{8}}}),
			pcrs: PCRSelectionList{
				PCRSelection{Hash: HashAlgorithmSHA256, Select: PCRSelectionData{8}},
				PCRSelection{Hash: HashAlgorithmSHA1, Select: PCRSelectionData{8}}},
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyPCR(sessionContext, data.digest, data.pcrs); err != nil {
				t.Fatalf("PolicyPCR failed: %v", err)
			}

			policyDigest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			pcrDigest := data.digest
			if len(pcrDigest) == 0 {
				pcrDigest = computePCRDigestFromTPM(t, tpm, HashAlgorithmSHA256, data.pcrs)
			}

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyPCR(pcrDigest, data.pcrs)

			if !bytes.Equal(policyDigest, trial.GetDigest()) {
				t.Errorf("Unexpected policy digest")
			}
		})
	}
}

func TestPolicyCommandCode(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	for _, data := range []struct {
		desc string
		code CommandCode
	}{
		{
			desc: "1",
			code: CommandUnseal,
		},
		{
			desc: "2",
			code: CommandNVChangeAuth,
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyCommandCode(data.code)

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyCommandCode(sessionContext, data.code); err != nil {
				t.Fatalf("PolicyPassword failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, trial.GetDigest()) {
				t.Errorf("Unexpected session digest")
			}
		})
	}
}

func TestPolicyCpHash(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	for _, data := range []struct {
		desc string
		data []byte
	}{
		{
			desc: "1",
			data: []byte("foo"),
		},
		{
			desc: "2",
			data: []byte("bar"),
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			h := crypto.SHA256.New()
			h.Write(data.data)
			cpHashA := h.Sum(nil)

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyCpHash(cpHashA)

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyCpHash(sessionContext, cpHashA); err != nil {
				t.Fatalf("PolicyCpHash failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, trial.GetDigest()) {
				t.Errorf("Unexpected session digest")
			}
		})
	}
}

func TestPolicyNameHash(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	for _, data := range []struct {
		desc string
		data []byte
	}{
		{
			desc: "1",
			data: []byte("foo"),
		},
		{
			desc: "2",
			data: []byte("bar"),
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			h := crypto.SHA256.New()
			h.Write(data.data)
			nameHash := h.Sum(nil)

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyNameHash(nameHash)

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyNameHash(sessionContext, nameHash); err != nil {
				t.Fatalf("PolicyNameHash failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, trial.GetDigest()) {
				t.Errorf("Unexpected session digest")
			}
		})
	}
}

func TestPolicyDuplicationSelect(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	for _, data := range []struct {
		desc          string
		objectData    []byte
		parentData    []byte
		includeObject bool
	}{
		{
			desc:          "1",
			objectData:    []byte("foo"),
			parentData:    []byte("bar"),
			includeObject: true,
		},
		{
			desc:          "2",
			objectData:    []byte("foo"),
			parentData:    []byte("bar"),
			includeObject: false,
		},
		{
			desc:          "3",
			objectData:    []byte("bar"),
			parentData:    []byte("foo"),
			includeObject: false,
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			h := crypto.SHA256.New()
			h.Write(data.objectData)
			objectName, _ := MarshalToBytes(HashAlgorithmSHA256, h.Sum(nil))

			h = crypto.SHA256.New()
			h.Write(data.parentData)
			newParentName, _ := MarshalToBytes(HashAlgorithmSHA256, h.Sum(nil))

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyDuplicationSelect(objectName, newParentName, data.includeObject)

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyDuplicationSelect(sessionContext, objectName, newParentName, data.includeObject); err != nil {
				t.Fatalf("PolicyDuplicationSelect failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, trial.GetDigest()) {
				t.Errorf("Unexpected session digest")
			}
		})
	}
}

func TestPolicyAuthorize(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	keyPublic := Public{
		Type:    ObjectTypeRSA,
		NameAlg: HashAlgorithmSHA256,
		Attrs:   AttrSensitiveDataOrigin | AttrUserWithAuth | AttrSign,
		Params: PublicParamsU{
			&RSAParams{
				Symmetric: SymDefObject{Algorithm: SymObjectAlgorithmNull},
				Scheme:    RSAScheme{Scheme: RSASchemeNull},
				KeyBits:   2048,
				Exponent:  uint32(key.PublicKey.E)}},
		Unique: PublicIDU{Digest(key.PublicKey.N.Bytes())}}
	keyContext, keyName, err := tpm.LoadExternal(nil, &keyPublic, HandleOwner)
	if err != nil {
		t.Fatalf("LoadExternal failed: %v", err)
	}
	defer flushContext(t, tpm, keyContext)

	for _, data := range []struct {
		desc        string
		policyRef   Nonce
		commandCode CommandCode
	}{
		{
			desc:        "1",
			commandCode: CommandNVChangeAuth,
		},
		{
			desc:        "2",
			commandCode: CommandObjectChangeAuth,
		},
		{
			desc:        "3",
			commandCode: CommandNVChangeAuth,
			policyRef:   Nonce("bar"),
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			staticTrial, err := ComputeAuthPolicy(HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("ComputeAuthPolicy failed: %v", err)
			}
			staticTrial.PolicyAuthorize(data.policyRef, keyName)

			dynamicTrial, err := ComputeAuthPolicy(HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("ComputeAuthPolicy failed: %v", err)
			}
			dynamicTrial.PolicyCommandCode(data.commandCode)
			dynamicTrial.PolicyAuthValue()

			approvedPolicy := dynamicTrial.GetDigest()

			h := HashAlgorithmSHA256.NewHash()
			h.Write(approvedPolicy)
			h.Write(data.policyRef)

			aHash := h.Sum(nil)

			s, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, aHash, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			if err != nil {
				t.Fatalf("Signing failed: %v", err)
			}

			signature := Signature{
				SigAlg:    SigSchemeAlgRSAPSS,
				Signature: SignatureU{Data: &SignatureRSAPSS{Hash: HashAlgorithmSHA256, Sig: PublicKeyRSA(s)}}}

			checkTicket, err := tpm.VerifySignature(keyContext, aHash, &signature)
			if err != nil {
				t.Fatalf("VerifySignature failed: %v", err)
			}

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyCommandCode(sessionContext, data.commandCode); err != nil {
				t.Fatalf("PolicyCommandCode failed: %v", err)
			}
			if err := tpm.PolicyAuthValue(sessionContext); err != nil {
				t.Fatalf("PolicyAuthValue failed: %v", err)
			}

			if err := tpm.PolicyAuthorize(sessionContext, approvedPolicy, data.policyRef, keyName, checkTicket); err != nil {
				t.Errorf("PolicyAuthorize failed: %v", err)
			}

			policyDigest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(policyDigest, staticTrial.GetDigest()) {
				t.Errorf("Unexpected policy digest")
			}
		})
	}
}

func TestPolicyAuthValue(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
	trial.PolicyAuthValue()

	authPolicy := trial.GetDigest()

	primary := createRSASrkForTesting(t, tpm, nil)
	defer flushContext(t, tpm, primary)

	template := Public{
		Type:       ObjectTypeKeyedHash,
		NameAlg:    HashAlgorithmSHA256,
		Attrs:      AttrFixedTPM | AttrFixedParent,
		AuthPolicy: authPolicy,
		Params:     PublicParamsU{&KeyedHashParams{Scheme: KeyedHashScheme{Scheme: KeyedHashSchemeNull}}}}
	sensitive := SensitiveCreate{Data: []byte("secret"), UserAuth: testAuth}
	outPrivate, outPublic, _, _, _, err := tpm.Create(primary, &sensitive, &template, nil, nil, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	objectContext, _, err := tpm.Load(primary, outPrivate, outPublic, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	defer flushContext(t, tpm, objectContext)

	objectContext.SetAuthValue(testAuth)

	for _, data := range []struct {
		desc   string
		tpmKey ResourceContext
		bind   ResourceContext
	}{
		{
			desc: "UnboundUnsalted",
		},
		{
			desc: "BoundUnsalted",
			bind: objectContext,
		},
		{
			desc:   "UnboundSalted",
			tpmKey: primary,
		},
		{
			desc:   "BoundSalted",
			tpmKey: primary,
			bind:   objectContext,
		},
	} {
		t.Run(data.desc, func(t *testing.T) {
			sessionContext, err := tpm.StartAuthSession(data.tpmKey, data.bind, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer verifyContextFlushed(t, tpm, sessionContext)

			if err := tpm.PolicyAuthValue(sessionContext); err != nil {
				t.Fatalf("PolicyAuthValue failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, authPolicy) {
				t.Errorf("Unexpected session digest")
			}

			if _, err := tpm.Unseal(objectContext, &Session{Context: sessionContext, AuthValue: testAuth}); err != nil {
				t.Errorf("Unseal failed: %v", err)
			}
		})
	}
}

func TestPolicyPassword(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
	trial.PolicyPassword()

	authPolicy := trial.GetDigest()

	primary := createRSASrkForTesting(t, tpm, nil)
	defer flushContext(t, tpm, primary)

	template := Public{
		Type:       ObjectTypeKeyedHash,
		NameAlg:    HashAlgorithmSHA256,
		Attrs:      AttrFixedTPM | AttrFixedParent,
		AuthPolicy: authPolicy,
		Params:     PublicParamsU{&KeyedHashParams{Scheme: KeyedHashScheme{Scheme: KeyedHashSchemeNull}}}}
	sensitive := SensitiveCreate{Data: []byte("secret"), UserAuth: testAuth}
	outPrivate, outPublic, _, _, _, err := tpm.Create(primary, &sensitive, &template, nil, nil, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	objectContext, _, err := tpm.Load(primary, outPrivate, outPublic, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	defer flushContext(t, tpm, objectContext)

	sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
	if err != nil {
		t.Fatalf("StartAuthSession failed: %v", err)
	}
	defer verifyContextFlushed(t, tpm, sessionContext)

	if err := tpm.PolicyPassword(sessionContext); err != nil {
		t.Fatalf("PolicyPassword failed: %v", err)
	}

	digest, err := tpm.PolicyGetDigest(sessionContext)
	if err != nil {
		t.Fatalf("PolicyGetDigest failed: %v", err)
	}

	if !bytes.Equal(digest, authPolicy) {
		t.Errorf("Unexpected session digest")
	}

	if _, err := tpm.Unseal(objectContext, &Session{Context: sessionContext, AuthValue: testAuth}); err != nil {
		t.Errorf("Unseal failed: %v", err)
	}
}

func TestPolicyNV(t *testing.T) {
	tpm := openTPMForTesting(t)
	defer closeTPM(t, tpm)

	primary := createRSASrkForTesting(t, tpm, nil)
	defer flushContext(t, tpm, primary)

	twentyFiveUint64 := make(Operand, 8)
	binary.BigEndian.PutUint64(twentyFiveUint64, 25)

	tenUint64 := make(Operand, 8)
	binary.BigEndian.PutUint64(tenUint64, 10)

	fortyUint32 := make(Operand, 4)
	binary.BigEndian.PutUint32(fortyUint32, 40)

	owner := tpm.OwnerHandleContext()

	for _, data := range []struct {
		desc      string
		pub       NVPublic
		prepare   func(*testing.T, HandleContext, interface{})
		operandB  Operand
		offset    uint16
		operation ArithmeticOp
	}{
		{
			desc: "UnsignedLE",
			pub: NVPublic{
				Index:   Handle(0x0181ffff),
				NameAlg: HashAlgorithmSHA256,
				Attrs:   MakeNVAttributes(AttrNVAuthWrite|AttrNVAuthRead, NVTypeOrdinary),
				Size:    8},
			prepare: func(t *testing.T, index HandleContext, auth interface{}) {
				if err := tpm.NVWrite(index, index, MaxNVBuffer(twentyFiveUint64), 0, auth); err != nil {
					t.Fatalf("NVWrite failed: %v", err)
				}
			},
			operandB:  twentyFiveUint64,
			offset:    0,
			operation: OpUnsignedLE,
		},
		{
			desc: "UnsignedGT",
			pub: NVPublic{
				Index:   Handle(0x0181ffff),
				NameAlg: HashAlgorithmSHA256,
				Attrs:   MakeNVAttributes(AttrNVAuthWrite|AttrNVAuthRead, NVTypeOrdinary),
				Size:    8},
			prepare: func(t *testing.T, index HandleContext, auth interface{}) {
				if err := tpm.NVWrite(index, index, MaxNVBuffer(twentyFiveUint64), 0, auth); err != nil {
					t.Fatalf("NVWrite failed: %v", err)
				}
			},
			operandB:  tenUint64,
			offset:    0,
			operation: OpUnsignedGT,
		},
		{
			desc: "Offset",
			pub: NVPublic{
				Index:   Handle(0x0181ffff),
				NameAlg: HashAlgorithmSHA256,
				Attrs:   MakeNVAttributes(AttrNVAuthWrite|AttrNVAuthRead, NVTypeOrdinary),
				Size:    8},
			prepare: func(t *testing.T, index HandleContext, auth interface{}) {
				if err := tpm.NVWrite(index, index, MaxNVBuffer(fortyUint32), 4, auth); err != nil {
					t.Fatalf("NVWrite failed: %v", err)
				}
			},
			operandB:  fortyUint32,
			offset:    4,
			operation: OpEq,
		},
	} {
		createIndex := func(t *testing.T, authValue Auth) HandleContext {
			if err := tpm.NVDefineSpace(owner, authValue, &data.pub, nil); err != nil {
				t.Fatalf("NVDefineSpace failed: %v", err)
			}
			index, err := tpm.GetOrCreateResourceContext(data.pub.Index)
			if err != nil {
				t.Fatalf("GetOrCreateResourceContext failed: %v", err)
			}
			return index
		}

		run := func(t *testing.T, index HandleContext, auth interface{}) {
			data.prepare(t, index, auth)

			trial, _ := ComputeAuthPolicy(HashAlgorithmSHA256)
			trial.PolicyNV(index.Name(), data.operandB, data.offset, data.operation)

			authPolicy := trial.GetDigest()

			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypePolicy, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			if err := tpm.PolicyNV(index, index, sessionContext, data.operandB, data.offset, data.operation, auth); err != nil {
				t.Fatalf("PolicyNV failed: %v", err)
			}

			digest, err := tpm.PolicyGetDigest(sessionContext)
			if err != nil {
				t.Fatalf("PolicyGetDigest failed: %v", err)
			}

			if !bytes.Equal(digest, authPolicy) {
				t.Errorf("Unexpected session digest")
			}
		}

		t.Run(data.desc+"/NoAuth", func(t *testing.T) {
			index := createIndex(t, nil)
			defer undefineNVSpace(t, tpm, index, owner, nil)
			run(t, index, nil)
		})

		t.Run(data.desc+"/UsePasswordAuth", func(t *testing.T) {
			index := createIndex(t, testAuth)
			defer undefineNVSpace(t, tpm, index, owner, nil)
			run(t, index, testAuth)
		})

		t.Run(data.desc+"/UseSessionAuth", func(t *testing.T) {
			index := createIndex(t, testAuth)
			defer undefineNVSpace(t, tpm, index, owner, nil)

			// Don't use a bound session as the name of index changes when it is written to for the first time
			sessionContext, err := tpm.StartAuthSession(nil, nil, SessionTypeHMAC, nil, HashAlgorithmSHA256)
			if err != nil {
				t.Fatalf("StartAuthSession failed: %v", err)
			}
			defer flushContext(t, tpm, sessionContext)

			session := &Session{Context: sessionContext, Attrs: AttrContinueSession,
				AuthValue: testAuth}
			run(t, index, session)
		})
	}
}
