FROM pgvector/pgvector:pg17

COPY migrations /docker-entrypoint-initdb.d/
RUN chmod -R a+rX /docker-entrypoint-initdb.d
