FROM pgvector/pgvector:pg18

COPY migrations /docker-entrypoint-initdb.d/
RUN chmod -R a+rX /docker-entrypoint-initdb.d
