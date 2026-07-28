// Package master implements the traffic-keeper master server.
//
// It hosts two logical planes:
//
//   - The control plane: node management, policy dispatch, single-user auth.
//   - The data plane: an HTTP upload receiver that counts bytes per agent and
//     discards request bodies, returning only a minimal response so that
//     downstream (download) traffic on the agent stays near zero.
package master
