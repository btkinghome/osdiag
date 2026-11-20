package sysinfo

import "testing"

func TestParseOSRelease(t *testing.T) {
	raw := `NAME="openEuler"
ID=openEuler
VERSION_ID="20.03"
PRETTY_NAME="openEuler 20.03 (LTS-SP3)"
# comment
EMPTY=
`
	kv := ParseOSRelease(raw)
	if kv["ID"] != "openEuler" || kv["VERSION_ID"] != "20.03" {
		t.Fatalf("解析错误: %#v", kv)
	}
	if kv["PRETTY_NAME"] != "openEuler 20.03 (LTS-SP3)" {
		t.Fatalf("引号未去掉: %q", kv["PRETTY_NAME"])
	}
	if _, ok := kv["# comment"]; ok {
		t.Fatal("注释不应成为键")
	}
}

func TestKernelBaselineOK(t *testing.T) {
	cases := map[string]bool{
		"4.19.90-2504.1.0.0322.250403.openeuler.x86_64": true,
		"4.18.0-80.el8.x86_64":                          false,
		"5.14.0-362.el9.x86_64":                         true,
		"3.10.0-1160.el7.x86_64":                        false,
		"6.1.0":                                         true,
		"unknown":                                       false,
	}
	for in, want := range cases {
		if got := KernelBaselineOK(in); got != want {
			t.Errorf("KernelBaselineOK(%q)=%v want %v", in, got, want)
		}
	}
}

func TestMachineArch(t *testing.T) {
	if MachineArch() == "" {
		t.Fatal("arch 为空")
	}
}
