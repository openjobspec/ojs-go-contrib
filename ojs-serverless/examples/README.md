# ojs-serverless Example

An AWS Lambda handler example for processing OJS jobs via SQS.

## Prerequisites

- [AWS SAM CLI](https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/install-sam-cli.html)
- Go 1.22+
- Docker (for local testing)

## Deploy

```bash
sam build
sam deploy --guided
```

## Local Testing

```bash
sam local invoke OJSWorkerFunction --event event.json
```

## Architecture

The SAM template (`template.yaml`) provisions:
- An SQS queue for receiving OJS jobs
- A dead-letter queue for failed jobs
- A Lambda function triggered by SQS events
- Partial batch failure reporting for efficient retries
