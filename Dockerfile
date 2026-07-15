# ---------- build stage ----------
FROM golang:1.26.5-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Build statically (CGO disabled) so we can use a scratch-like final image.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitindex .

# ---------- runtime stage ----------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/gitindex /app/gitindex

ENV PORT=6541
EXPOSE 6541
USER nonroot:nonroot

ENTRYPOINT ["/app/gitindex"]
