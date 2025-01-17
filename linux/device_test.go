// Copyright 2023 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package linux_test

import (
	"os/exec"
	"path/filepath"

	. "gopkg.in/check.v1"

	. "github.com/canonical/go-tpm2/linux"
	"github.com/canonical/go-tpm2/ppi"
	"github.com/canonical/go-tpm2/testutil"
)

type deviceSuite struct {
	testutil.BaseTest
}

var _ = Suite(&deviceSuite{})

func (s *deviceSuite) mockPPIBackend(c *C, path string) *PpiImpl {
	impl, err := NewPPI(path)
	c.Assert(err, IsNil)
	return impl
}

func (s *deviceSuite) unpackTarball(c *C, path string) string {
	dir := c.MkDir()

	cmd := exec.Command("tar", "xaf", path, "-C", dir)
	c.Assert(cmd.Run(), IsNil)

	return dir
}

func (s *deviceSuite) TestListTPMDevicesTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
	})
}

func (s *deviceSuite) TestListTPMDevicesTPM2OldKernel(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-old-kernel-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
	})
}

func (s *deviceSuite) TestListTPMDevicesNoDevices(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/no-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw(nil))
}

func (s *deviceSuite) TestListTPMDevicesTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm0"), 1, 0, nil),
	})
}

func (s *deviceSuite) TestListTPMDevicesMixedTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm2-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
		NewMockTPMDeviceRaw("/dev/tpm1", filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm1"), 1, 1, nil),
	})
}

func (s *deviceSuite) TestListTPMDevicesMixedTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm1-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm0"), 1, 0, nil),
		NewMockTPMDeviceRaw("/dev/tpm1", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm1"), 2, 1, nil),
	})
}

func (s *deviceSuite) TestListTPMDevicesTPM2Multiple(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/multiple-tpm2-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0/ppi"))

	devices, err := ListTPMDevices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0"), 2, 0, pp),
		NewMockTPMDeviceRaw("/dev/tpm1", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm1"), 2, 1, nil),
	})
}

func (s *deviceSuite) TestListTPM2DevicesTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
	})
}

func (s *deviceSuite) TestListTPM2DevicesTPM2OldKernel(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-old-kernel-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
	})
}

func (s *deviceSuite) TestListTPM2DevicesNoDevices(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/no-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw(nil))
}

func (s *deviceSuite) TestListTPM2DevicesTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw(nil))
}

func (s *deviceSuite) TestListTPM2DevicesMixedTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm2-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp),
	})
}

func (s *deviceSuite) TestListTPM2DevicesMixedTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm1-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm1", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm1"), 2, 1, nil),
	})
}

func (s *deviceSuite) TestListTPM2DevicesTPM2Multiple(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/multiple-tpm2-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0/ppi"))

	devices, err := ListTPM2Devices()
	c.Check(err, IsNil)
	c.Check(devices, DeepEquals, []*TPMDeviceRaw{
		NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0"), 2, 0, pp),
		NewMockTPMDeviceRaw("/dev/tpm1", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm1"), 2, 1, nil),
	})
}

func (s *deviceSuite) TestDefaultTPMDeviceTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPMDeviceTPM2OldKernel(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-old-kernel-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPMDeviceNoDevices(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/no-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	_, err := DefaultTPMDevice()
	c.Check(err, Equals, ErrNoTPMDevices)
}

func (s *deviceSuite) TestDefaultTPMDeviceTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm0"), 1, 0, nil))
}

func (s *deviceSuite) TestDefaultTPMDeviceMixedTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm2-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPMDeviceMixedTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm1-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm0"), 1, 0, nil))
}

func (s *deviceSuite) TestDefaultTPMDeviceTPM2Multiple(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/multiple-tpm2-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0/ppi"))

	device, err := DefaultTPMDevice()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPM2DeviceTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPM2Device()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPM2DeviceTPM2OldKernel(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-old-kernel-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPM2Device()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPM2DeviceNoDevices(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/no-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	_, err := DefaultTPM2Device()
	c.Check(err, Equals, ErrNoTPMDevices)
}

func (s *deviceSuite) TestDefaultTPM2DeviceTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	_, err := DefaultTPM2Device()
	c.Check(err, Equals, ErrDefaultNotTPM2Device)
}

func (s *deviceSuite) TestDefaultTPM2DeviceMixedTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm2-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi"))

	device, err := DefaultTPM2Device()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestDefaultTPM2DeviceMixedTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/mixed-devices-tpm1-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	_, err := DefaultTPM2Device()
	c.Check(err, Equals, ErrDefaultNotTPM2Device)
}

func (s *deviceSuite) TestDefaultTPM2DeviceTPM2Multiple(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/multiple-tpm2-devices-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	pp := s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0/ppi"))

	device, err := DefaultTPM2Device()
	c.Check(err, IsNil)
	c.Check(device, DeepEquals, NewMockTPMDeviceRaw("/dev/tpm0", filepath.Join(sysfsPath, "devices/platform/MSFT0101:00/tpm/tpm0"), 2, 0, pp))
}

func (s *deviceSuite) TestTPMDeviceMethodsTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)
	c.Check(device.Path(), Equals, "/dev/tpm0")
	c.Check(device.SysfsPath(), Equals, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0"))
	c.Check(device.MajorVersion(), Equals, 2)
}

func (s *deviceSuite) TestTPMDeviceMethodsTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)
	c.Check(device.Path(), Equals, "/dev/tpm0")
	c.Check(device.SysfsPath(), Equals, filepath.Join(sysfsPath, "devices/platform/SMO3324:00/tpm/tpm0"))
	c.Check(device.MajorVersion(), Equals, 1)
}

func (s *deviceSuite) TestTPMDeviceRawResourceManagedDeviceTPM2(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)

	rm, err := device.ResourceManagedDevice()
	c.Check(err, IsNil)
	c.Check(rm, DeepEquals, NewMockTPMDeviceRM("/dev/tpmrm0", filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpmrm/tpmrm0"), 2, device))
	c.Check(rm.RawDevice(), Equals, device)
}

func (s *deviceSuite) TestTPMDeviceRawResourceManagedDeviceTPM2NoRM(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-no-rm-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)

	_, err = device.ResourceManagedDevice()
	c.Check(err, Equals, ErrNoResourceManagedDevice)
}

func (s *deviceSuite) TestTPMDeviceRawResourceManagedDeviceTPM1(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)

	_, err = device.ResourceManagedDevice()
	c.Check(err, Equals, ErrNoResourceManagedDevice)
}

func (s *deviceSuite) TestTPMDeviceRawPhysicalPresenceInterface(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm2-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	expected := ppi.NewPPI(s.mockPPIBackend(c, filepath.Join(sysfsPath, "devices/platform/STM0125:00/tpm/tpm0/ppi")))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)

	pp, err := device.PhysicalPresenceInterface()
	c.Assert(err, IsNil)
	c.Check(pp, DeepEquals, expected)
}

func (s *deviceSuite) TestTPMDeviceRawPhysicalPresenceInterfaceNone(c *C) {
	sysfsPath := s.unpackTarball(c, "testdata/tpm1-device-sysfs.tar")
	s.AddCleanup(MockSysfsPath(sysfsPath))

	device, err := DefaultTPMDevice()
	c.Assert(err, IsNil)

	_, err = device.PhysicalPresenceInterface()
	c.Assert(err, Equals, ErrNoPhysicalPresenceInterface)
}
