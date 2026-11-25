// Package mailer composes provider-neutral Mealbot messages and delivers them
// through a context-bound Mailgun v4 adapter. It never reads process-global
// configuration; callers inject validated settings and provider clients.
package mailer
