// Package listenfds hands listening sockets from a parent process to a child
// as inherited file descriptors, in the systemd socket-activation protocol:
// descriptors from 3 on, counted by LISTEN_FDS and named by LISTEN_FDNAMES.
//
// The integration test harness binds every port a `muster serve` instance
// listens on and passes the listeners down, so the instance never binds a
// port the harness closed first. A socket closed while the harness forks
// another child lives on in that child until its exec, and a rebind of the
// port in that window fails with "address already in use". systemd starts
// muster the same way through muster.socket.
//
// LISTEN_PID is optional: systemd sets it to the activated process, an
// exec.Cmd cannot know its child's PID before the fork. Listeners announced
// for another PID are not taken.
package listenfds
