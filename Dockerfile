# --- build stage -------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

# cache module downloads separately from source changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dashboard .

# --- runtime stage ------------------------------------------------------------
# distroless: no shell, no package manager — smaller attack surface than alpine
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/dashboard /app/dashboard

USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/app/dashboard"]
