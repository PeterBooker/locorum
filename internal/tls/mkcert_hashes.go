package tls

// mkcertSHA256 maps "GOOS/GOARCH" to the lower-case hex SHA-256 of the
// mkcert binary published at the pinned MkcertVersion. Verified once
// against the GitHub-hosted release artefacts; bumping MkcertVersion
// requires regenerating this table from the new release.
//
// The table is the security pin: a trojaned upstream — whether via a
// compromise of dl.filippo.io, an attacker-injected redirect, or a
// rogue CA in the system trust store enabling MITM — produces a hash
// mismatch and a refused install. Without this pin, HTTPS-only is the
// only protection between an arbitrary tampering of the binary and the
// user being asked to install a root CA into the OS trust store from
// it (the worst attack surface in the app).
//
// To regenerate after a version bump:
//
//	for asset in mkcert-vX.Y.Z-linux-amd64 …; do
//	    curl -fsSLO https://github.com/FiloSottile/mkcert/releases/download/vX.Y.Z/$asset
//	done && sha256sum mkcert-vX.Y.Z-*
var mkcertSHA256 = map[string]string{
	"darwin/amd64":  "a32dfab51f1845d51e810db8e47dcf0e6b51ae3422426514bf5a2b8302e97d4e",
	"darwin/arm64":  "c8af0df44bce04359794dad8ea28d750437411d632748049d08644ffb66a60c6",
	"linux/amd64":   "6d31c65b03972c6dc4a14ab429f2928300518b26503f58723e532d1b0a3bbb52",
	"linux/arm":     "2f22ff62dfc13357e147e027117724e7ce1ff810e30d2b061b05b668ecb4f1d7",
	"linux/arm64":   "b98f2cc69fd9147fe4d405d859c57504571adec0d3611c3eefd04107c7ac00d0",
	"windows/amd64": "d2660b50a9ed59eada480750561c96abc2ed4c9a38c6a24d93e30e0977631398",
	"windows/arm64": "793747256c562622d40127c8080df26add2fb44c50906ce9db63b42a5280582e",
}
