# ---------- build stage ----------
FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Build statically (CGO disabled) so we can use a scratch-like final image.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitindex .

# ---------- runtime stage ----------
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /app
COPY --from=build /out/gitindex /app/gitindex

ENV PORT=6541
EXPOSE 6541
USER nonroot:nonroot

ENTRYPOINT ["/app/gitindex"]
