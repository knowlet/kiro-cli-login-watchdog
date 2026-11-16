# Telegram notification redirects

Base: `4cfdce7586f45b1cdee9b7103a31a38b0929a7a0`.

The notifier's default Go HTTP client follows redirects. The request body
contains a chat ID and the device-flow login code/URL. Go preserves the POST
body on 307/308 when GetBody is available, as it is for strings.Reader. A
redirect can therefore forward those credentials to a different destination.
301/302/303 may also yield a false delivery success if the redirected response
contains `{"ok":true}`. This is conditional on receiving a redirect, not a
claim that Telegram currently issues malicious redirects or was compromised.

Reference: https://pkg.go.dev/net/http#Client.Do and
https://pkg.go.dev/net/http#Client.CheckRedirect.

Reject all redirects with http.ErrUseLastResponse. The original 3xx response
then goes through the existing sanitized non-2xx error path. No retry,
recipient, endpoint, timeout, proxy, or credential handling is otherwise changed.

A synthetic RoundTripper regression covers 301, 302, 303, 307 and 308. On the
unmodified base, all five cases follow untrusted.invalid and incorrectly return
success; the 307/308 requests retain POST. With the fix, all cases make exactly
one request and return an error even when the redirect body says ok=true.
Tests do not contact Telegram or transmit any real login code or token.
