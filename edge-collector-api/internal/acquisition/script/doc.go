// Package script contains the controlled Starlark execution core for dynamic
// acquisition transactions.
//
// The package deliberately stops at the Host interface. It does not know how
// a channel, device, or Modbus session is scheduled. A later acquisition
// adapter can provide those details without changing the script language or
// its resource accounting.
package script
