// Stub the awslambda streaming global so handler.ts can be imported in tests.
// In production, this global is injected by the Lambda runtime.
// Shared with scripts/invoke-poster-local.ts, which hits the same wall.
import { stubAwsLambdaGlobal } from "./awslambda-stub.js";

stubAwsLambdaGlobal();
