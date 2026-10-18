# Builds the auracle Discord bot as a small, non-root image.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/auracle ./cmd/auracle

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/auracle /auracle
USER nonroot:nonroot
ENTRYPOINT ["/auracle"]
