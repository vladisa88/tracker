FROM postgres:17-alpine
RUN apk add --no-cache python3 aws-cli && mkdir /state && chown postgres:postgres /state
COPY backup.py /backup.py
USER postgres
ENV BACKUP_STATE_DIR=/state AWS_DEFAULT_REGION=us-east-1 AWS_PAGER="" AWS_EC2_METADATA_DISABLED=true
ENTRYPOINT ["python3", "/backup.py"]
CMD ["daemon"]
