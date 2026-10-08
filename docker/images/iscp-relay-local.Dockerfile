# The lab runner compiles relayd from the ISCP module version locked in go.mod.
# Its private build context contains that unmodified-source Linux binary only.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --chmod=0555 relayd /relayd
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/relayd"]
