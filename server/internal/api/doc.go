// Package api is the HTTP layer of the server (spec §6): routing under /api,
// Bearer authentication, the JSON error envelope, request logging and the
// wire DTOs.
//
// The package never serialises store.Job directly. Jobs carry request headers
// (possibly cookies and tokens) that must not leave the server, so every job
// response goes through JobDTO, which has no headers field.
package api
