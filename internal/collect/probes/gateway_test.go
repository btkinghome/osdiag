package probes

import (
	"strings"
	"testing"
)

func TestParseDefaultGateway(t *testing.T) {
	header := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	cases := []struct {
		name      string
		table     string
		wantGW    string
		wantIface string
		wantErr   bool
	}{
		{
			name: "标准默认路由",
			table: header +
				"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			wantGW: "192.168.2.1", wantIface: "eth0",
		},
		{
			name: "多默认路由取最小 metric",
			table: header +
				"eth0\t00000000\t0102A8C0\t0003\t0\t0\t200\t00000000\t0\t0\t0\n" +
				"eth1\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			wantGW: "192.168.1.1", wantIface: "eth1",
		},
		{
			name: "没有默认路由",
			table: header +
				"eth0\t0001A8C0\t0102A8C0\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
			wantErr: true,
		},
		{
			name: "默认路由缺少 G 标志",
			table: header +
				"eth0\t00000000\t0102A8C0\t0001\t0\t0\t100\t00000000\t0\t0\t0\n",
			wantErr: true,
		},
		{
			name: "非法网关字段被跳过",
			table: header +
				"eth0\t00000000\tZZZZZZZZ\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
				"eth1\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			wantGW: "192.168.1.1", wantIface: "eth1",
		},
		{
			name:    "缺少 metric 列",
			table:   header + "eth0\t00000000\t0102A8C0\t0003\n",
			wantErr: true,
		},
		{
			name:    "空表",
			table:   header,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw, iface, err := parseDefaultGateway(strings.NewReader(tc.table))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际 gateway=%q iface=%q", gw, iface)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDefaultGateway: %v", err)
			}
			if gw != tc.wantGW || iface != tc.wantIface {
				t.Errorf("得到 (%q,%q), want (%q,%q)", gw, iface, tc.wantGW, tc.wantIface)
			}
		})
	}
}

func TestHexLEToIPv4(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"0102A8C0", "192.168.2.1", false},
		{"00000000", "0.0.0.0", false},
		{"FFFFFFFF", "255.255.255.255", false},
		{"0102A8C", "", true},
		{"ZZZZZZZZ", "", true},
	}
	for _, tc := range cases {
		got, err := hexLEToIPv4(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("hexLEToIPv4(%q) 期望错误，得到 %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("hexLEToIPv4(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("hexLEToIPv4(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
