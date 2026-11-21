# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mealbot ./cmd/mealbot

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/mealbot /app/bin/mealbot
COPY static /app/static
COPY csv /app/csv
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/bin/mealbot"]
