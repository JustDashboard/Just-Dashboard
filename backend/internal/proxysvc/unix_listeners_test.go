package proxysvc

import "testing"

// The table has hundreds of rows on an ordinary machine and three kinds that
// are not a listening file: connected sockets, sockets with no name, and
// abstract ones whose name is not a path.
func TestParseUnixSocketTableKeepsListeningPaths(t *testing.T) {
	table := `Num       RefCount Protocol Flags    Type St Inode Path
0000000000000000: 00000003 00000000 00000000 0001 03 475833310 /run/containerd/s/dc8bad
0000000000000000: 00000003 00000000 00000000 0002 03 96750834
0000000000000000: 00000002 00000000 00010000 0001 01 455885649 /var/run/postgresql/.s.PGSQL.5438
0000000000000000: 00000002 00000000 00010000 0001 01 455885650 @/tmp/.X11-unix/X0
0000000000000000: 00000002 00000000 00010000 0001 01 455885651 /run/my app/with space.sock
0000000000000000: 00000002 00000000 00010000 0005 01 455885652 /run/udev/control
garbage
`
	rows := parseUnixSocketTable([]byte(table))
	want := []unixSocketRow{
		{455885649, "/var/run/postgresql/.s.PGSQL.5438"},
		{455885651, "/run/my app/with space.sock"},
		{455885652, "/run/udev/control"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if got := parseUnixSocketTable(nil); len(got) != 0 {
		t.Errorf("an empty table gave %+v", got)
	}
}
