// Package agent implements the traffic-keeper agent.
//
// The agent registers with the master using a one-time token, maintains a
// heartbeat, periodically polls its policy, and runs the upload executor:
// generating random (incompressible) data and PUT-ing it to the master data
// plane to keep the host VPS's upstream traffic non-zero while minimizing
// downstream traffic.
package agent
