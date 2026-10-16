# Target repos and their images are private by default

Target repos are created private, which means the first publish to GHCR
inherits that visibility: the image is private too, and pulling it needs a
`ghcr.io` login with a PAT. FastCI keeps private as the default and documents
the consequence, rather than defaulting to public.

Considered and rejected: defaulting to public. GHCR package visibility is
inherited on first publish and can only be changed by hand afterwards, so the
tool cannot flip a private image to public later -- the default has to be the
safe one.
