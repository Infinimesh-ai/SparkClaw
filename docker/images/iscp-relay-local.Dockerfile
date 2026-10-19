# Explicit independently built ISCP fork executables; no SparkClaw service copy.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --chmod=0555 relayd /relayd
COPY --chmod=0555 maintenance /maintenance
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/relayd"]
