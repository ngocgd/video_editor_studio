// Package youtube is the single client for every Google YouTube call made
// by the API and the worker: the Data API metadata calls, the resumable
// video upload and the quota ledger that gates them. Every call reserves
// its quota cost first (see Ledger) so the daily pool is never overrun.
package youtube
