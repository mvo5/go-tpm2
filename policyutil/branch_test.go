// Copyright 2023 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package policyutil_test

import (
	"crypto"
	"io"
	"strconv"

	. "gopkg.in/check.v1"

	"github.com/canonical/go-tpm2"
	"github.com/canonical/go-tpm2/internal/testutil"
	internal_testutil "github.com/canonical/go-tpm2/internal/testutil"
	. "github.com/canonical/go-tpm2/policyutil"
)

func hash(alg crypto.Hash, data string) []byte {
	h := alg.New()
	io.WriteString(h, data)
	return h.Sum(nil)
}

type branchSuite struct{}

func (s *branchSuite) checkPolicyOrTree(c *C, alg tpm2.HashAlgorithmId, digests tpm2.DigestList, tree *PolicyOrTree) (finalDigest tpm2.Digest, depth int) {
	leafNodes := tree.LeafNodes()
	for i, digest := range digests {
		var first bool
		if i == 0 {
			first = true
		}

		d := 0

		node := leafNodes[i>>3]
		for node != nil {
			d += 1

			nodeDigests := node.Digests()
			c.Check(nodeDigests[i&0x7], DeepEquals, digest)

			ta := &TaggedHash{HashAlg: alg, Digest: make(tpm2.Digest, alg.Size())}
			trial := NewComputePolicySession(ta)
			if len(nodeDigests) == 1 {
				nodeDigests = tpm2.DigestList{nodeDigests[0], nodeDigests[0]}
			}
			trial.PolicyOR(nodeDigests)

			digest = ta.Digest
			node = node.Parent()
			i = i >> 3
		}

		if first {
			finalDigest = digest
			depth = d
		} else {
			c.Check(digest, DeepEquals, finalDigest)
			c.Check(d, Equals, depth)
		}
	}

	return finalDigest, depth
}

func (s *branchSuite) policyOrTreeBranchDigestLists(c *C, tree *PolicyOrTree, n int) (out []tpm2.DigestList) {
	leafNodes := tree.LeafNodes()

	node := leafNodes[n>>3]
	for node != nil {
		out = append(out, node.Digests())
		node = node.Parent()
	}

	return out
}

var _ = Suite(&branchSuite{})

type testNewPolicyOrTreeData struct {
	alg     tpm2.HashAlgorithmId
	digests tpm2.DigestList

	depth    int
	expected tpm2.Digest
}

func (s *branchSuite) testNewPolicyOrTree(c *C, data *testNewPolicyOrTreeData) {
	tree, err := NewPolicyOrTree(data.alg, data.digests)
	c.Assert(err, IsNil)

	policy, depth := s.checkPolicyOrTree(c, data.alg, data.digests, tree)
	c.Check(policy, DeepEquals, data.expected)
	c.Check(depth, Equals, data.depth)
}

func (s *branchSuite) TestNewPolicyOrTreeSingleDigest(c *C) {
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  tpm2.DigestList{hash(crypto.SHA256, "foo")},
		depth:    1,
		expected: internal_testutil.DecodeHexString(c, "51d05afe8c2bbc42a2c1f540d7390b0228cd0d59d417a8e765c28af6f43f024c")})
}

func (s *branchSuite) TestNewPolicyOrTreeDepth1(c *C) {
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg: tpm2.HashAlgorithmSHA256,
		digests: tpm2.DigestList{
			hash(crypto.SHA256, "1"),
			hash(crypto.SHA256, "2"),
			hash(crypto.SHA256, "3"),
			hash(crypto.SHA256, "4"),
			hash(crypto.SHA256, "5")},
		depth:    1,
		expected: testutil.DecodeHexString(c, "5e5a5c8790bd34336f2df51c216e072ca52bd9c0c2dc67e249d5952aa81aecfa")})
}

func (s *branchSuite) TestNewPolicyOrTreeDepth2(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 26; i++ {
		digests = append(digests, hash(crypto.SHA256, strconv.Itoa(i)))
	}
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  digests,
		depth:    2,
		expected: testutil.DecodeHexString(c, "84be2df61f929c0afca3bcec125f7365fd825b410a150019e250b0dfb25110cf")})
}

func (s *branchSuite) TestNewPolicyOrTreeSHA1(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 26; i++ {
		digests = append(digests, hash(crypto.SHA1, strconv.Itoa(i)))
	}
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg:      tpm2.HashAlgorithmSHA1,
		digests:  digests,
		depth:    2,
		expected: testutil.DecodeHexString(c, "dddd1fd38995710c4aa703599b9741e729ac9ceb")})
}

func (s *branchSuite) TestNewPolicyOrTreeDepth3(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 201; i++ {
		digests = append(digests, hash(crypto.SHA256, strconv.Itoa(i)))
	}
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  digests,
		depth:    3,
		expected: testutil.DecodeHexString(c, "9c1cb2f1722a0a06f5e6774a9628cabce76572b0f2201bf66002a9eb2dfd6f11")})
}

func (s *branchSuite) TestNewPolicyOrTreeDepth4(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 1601; i++ {
		digests = append(digests, hash(crypto.SHA256, strconv.Itoa(i)))
	}
	s.testNewPolicyOrTree(c, &testNewPolicyOrTreeData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  digests,
		depth:    4,
		expected: testutil.DecodeHexString(c, "6f2ccbe268c9b3324c0922fcc2ccd760f6a7d264b7f61dccd3fba21f98412f85")})
}

func (s *branchSuite) TestNewPolicyOrTreeNoDigests(c *C) {
	_, err := NewPolicyOrTree(tpm2.HashAlgorithmSHA256, nil)
	c.Check(err, ErrorMatches, "no digests")
}

func (s *branchSuite) TestNewPolicyOrTreeTooManyDigests(c *C) {
	_, err := NewPolicyOrTree(tpm2.HashAlgorithmSHA256, make(tpm2.DigestList, 5000))
	c.Check(err, ErrorMatches, "too many digests")
}

type testPolicyOrTreeSelectBranchData struct {
	alg      tpm2.HashAlgorithmId
	digests  tpm2.DigestList
	selected int
}

func (s *branchSuite) testPolicyOrTreeSelectBranch(c *C, data *testPolicyOrTreeSelectBranchData) {
	tree, err := NewPolicyOrTree(data.alg, data.digests)
	c.Assert(err, IsNil)

	policy, depth := s.checkPolicyOrTree(c, data.alg, data.digests, tree)

	var expected []PolicyElementRunner
	lists := s.policyOrTreeBranchDigestLists(c, tree, data.selected)
	for _, digests := range lists {
		var hashList []TaggedHashList
		for _, digest := range digests {
			hashList = append(hashList, TaggedHashList{{HashAlg: data.alg, Digest: digest}})
		}

		expected = append(expected, &PolicyOR{HashList: hashList})
	}
	c.Assert(expected, internal_testutil.LenEquals, depth)

	ta := &TaggedHash{HashAlg: data.alg, Digest: make(tpm2.Digest, data.alg.Size())}
	trial := NewComputePolicySession(ta)
	trial.PolicyOR(lists[len(lists)-1])
	c.Check(ta.Digest, DeepEquals, policy)

	elements := tree.SelectBranch(data.selected)
	c.Check(elements, DeepEquals, expected)
}

func (s *branchSuite) TestPolicyOrTreeSelectBranchSingleDigest(c *C) {
	s.testPolicyOrTreeSelectBranch(c, &testPolicyOrTreeSelectBranchData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  tpm2.DigestList{hash(crypto.SHA256, "foo")},
		selected: 0})
}

func (s *branchSuite) TestPolicyOrTreeSelectBranchDepth1(c *C) {
	s.testPolicyOrTreeSelectBranch(c, &testPolicyOrTreeSelectBranchData{
		alg: tpm2.HashAlgorithmSHA256,
		digests: tpm2.DigestList{
			hash(crypto.SHA256, "1"),
			hash(crypto.SHA256, "2"),
			hash(crypto.SHA256, "3"),
			hash(crypto.SHA256, "4"),
			hash(crypto.SHA256, "5")},
		selected: 4})
}

func (s *branchSuite) TestPolicyOrTreeSelectBranchDepth2(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 26; i++ {
		digests = append(digests, hash(crypto.SHA256, strconv.Itoa(i)))
	}
	s.testPolicyOrTreeSelectBranch(c, &testPolicyOrTreeSelectBranchData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  digests,
		selected: 22})
}

func (s *branchSuite) TestPolicyOrTreeSelectBranchDepth3(c *C) {
	var digests tpm2.DigestList
	for i := 1; i < 201; i++ {
		digests = append(digests, hash(crypto.SHA256, strconv.Itoa(i)))
	}
	s.testPolicyOrTreeSelectBranch(c, &testPolicyOrTreeSelectBranchData{
		alg:      tpm2.HashAlgorithmSHA256,
		digests:  digests,
		selected: 150})
}
