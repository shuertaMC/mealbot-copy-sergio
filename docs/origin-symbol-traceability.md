# Origin symbol traceability

This is the deletion record for every original first-party symbol named by the binding specification. “Consolidated” means the old symbol intentionally has no wrapper: its behavior lives at the exact replacement. Named tests use direct assertions; PostgreSQL behavior is additionally exercised by `TestPostgresRepositoryIntegration` and `TestLegacyPairCountsMigration` when the disposable-database gate is enabled.

## `server.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `Middleware` | `httpapi.Middleware`, `httpapi.RouterConfig` | `TestRouterMiddlewareOrderStaticAndPrecedence` |
| `Middleware.Apply` | route wrapping in `httpapi.NewRouter` | `TestRouterMiddlewareOrderStaticAndPrecedence` |
| `Middleware.ApplyFake` | injected identity `httpapi.Middleware` | `TestRouterConstructionValidation`; `TestComposedHTTPServerAuthenticatedOrganizationSmokeAndStatic` |
| `runTestSequence` | removed manual harness; `pairing.Scheduler.ProcessRound` | `TestSchedulerClaimsDeliversThenPersists`; `TestSchedulerZeroSingletonAndTriplePolicies` |
| `main` | `cmd/mealbot.main`, `run`, `app.ParseArgs`, `app.Execute` | `TestRunExitContract`; `TestParseArgs`; `TestExecutePairMigrateAndGracefulServer` |

## `auth.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `InvalidAccessToken` | `auth.ErrInvalidToken` and stable private `writeUnauthorized` response | `TestMiddlewareAuthorizationContract`; `TestMiddlewareInvalidTokenAndNilValidator` |
| `Issuer` | `config.Auth0.Issuer` | `TestFromMapParsesCompleteProductionConfiguration`; `TestValidatorClaimsAndKeyIdentifier` |
| `Audience` | `config.Auth0.Audience` | `TestValidatorClaimsAndKeyIdentifier` |
| `JSONWebKeySet` | `config.Auth0.JWKSURL` | `TestFromMapParsesCompleteProductionConfiguration`; `TestNewJWKSKeySourceRejectsInvalidConfiguration` |
| `CustomJWTMiddleware` | `auth.Middleware` plus `auth.Validator` | `TestMiddlewareAuthorizationContract` |
| `CustomJWTMiddleware.Handler` | `auth.Middleware.Handler` | `TestMiddlewareAuthorizationContract`; `TestMiddlewareOptionsBypassesAuthentication` |
| `CustomJWTMiddleware.CheckJWT` | `auth.Validator.Validate` plus private `bearerToken` | `TestValidatorClaimsAndKeyIdentifier`; `TestMiddlewareAuthorizationContract` |
| `Jwks` | private `auth.jwksDocument` | `TestJWKSKeySourceParsesModulusAndCertificate`; `TestJWKSKeySourceRejectsMalformedKeys` |
| `JSONWebKeys` | private `auth.jwkDocument` | `TestJWKSKeySourceRejectsMalformedKeys` |
| `GetFakeAuthHandler` | injected identity `httpapi.Middleware` | `TestRouterConstructionValidation` |
| `GetAuthHandler` | `app.ProductionFactories().NewAuthenticator`, composing the JWKS source, validator, and middleware | `TestProductionFactoriesAndRandomAreConstructible`; `TestMiddlewareAuthorizationContract` |
| `getPEMCertificate` | `auth.JWKSKeySource.Key` plus private `parseJWK` | `TestJWKSKeySourceParsesModulusAndCertificate`; `TestJWKSKeySourceRejectsMalformedKeys` |
| `verifyAudience` | `jwt/v5` audience validation configured by `auth.NewValidator` | `TestValidatorClaimsAndKeyIdentifier` |

## `cors.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `AccessControlAllowHeaders` | private `httpapi.allowHeaders` and `allowedCORSHeaders` | `TestCORSAllowedOriginDelegatesAndSetsContract`; `TestCORSPreflightRejectsUnsupportedRequest` |
| `GetCorsHandler` | `httpapi.CORS.Handler` | `TestCORSPreflightShortCircuits`; `TestCORSRejectsDisallowedOrigin`; `TestCORSAllowedOriginDelegatesAndSetsContract` |

## `db.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `DuplicateKeyErr` | `domain.ErrConflict` through private `postgres.mapDatabaseError` | `TestMapDatabaseError`; `TestOrganizationRepositoryMethods` |
| `LocalDBConnection` | immutable `config.Database` plus private `postgres.connectionString` | `TestConnectionString`; `TestFromMapRejectsInvalidValues` |

## `log.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `LogAndWriteErr` | private `httpapi.responder.failure`/`json` with `slog` | `TestMembersHandlerGetContracts` |
| `LogAndWrite` | private `httpapi.responder.json` and `text` | `TestOrganizationsHandlerContracts`; `TestRoundHandlersContracts` |
| `LogAndWriteStatusBadRequest` | typed `domain.ErrValidation` mapping in private `httpapi.publicError` | `TestMembersHandlerGetContracts`; `TestRoundHandlersValidationAndErrorMapping` |
| `LogAndWriteStatusInternalServerError` | generic `500` mapping in private `httpapi.publicError` | `TestMembersHandlerGetContracts`; `TestCreateOrganizationHandlerContracts` |

## `utils.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `getQueryParam` | `httpapi.QueryValue` | `TestQueryValueRequiresExactlyOneNonEmptyValue` |
| `getQueryParams` | `httpapi.QueryValues` | `TestQueryValuesPreservesKeyOrder`; `TestRoundHandlersValidationAndErrorMapping` |

## `org.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `Organization` | `domain.Organization` | `TestOrganizationConstructionAndValidation` |
| `CreateOrganizationRequestBody` | private `httpapi.createOrganizationRequest` | `TestCreateOrganizationHandlerContracts` |
| `SetCrossMatchTraitRequestBody` | private `httpapi.setCrossMatchTraitRequest` | `TestCrossMatchTraitHandlerSetClearAndErrors` |
| `GetOrganizationsHandler` | `httpapi.NewOrganizationsHandler` | `TestOrganizationsHandlerContracts` |
| `CreateOrganizationHandler` | `httpapi.NewCreateOrganizationHandler` | `TestCreateOrganizationHandlerContracts` |
| `CrossMatchTraitHandler` | `httpapi.NewCrossMatchTraitHandler` | `TestCrossMatchTraitHandlerSetClearAndErrors` |
| `getOrganizations` | `OrganizationService.ListOrganizationNames`, `Repository.ListOrganizations` | `TestOrganizationServiceValidationOrderingAndClearing`; `TestOrganizationRepositoryMethods` |
| `createOrganization` | `OrganizationService.Create`, `Repository.CreateOrganization` | `TestCreateOrganizationHandlerContracts`; `TestOrganizationRepositoryMethods` |
| `GetCrossMatchTrait` | `Repository.CrossMatchTrait` through consumer interfaces | `TestMemberServiceGetDeterministicOrderingAndNullableTrait`; `TestOrganizationRepositoryMethods` |
| `setCrossMatchTrait` | `OrganizationService.SetCrossMatchTrait`, `Repository.UpdateCrossMatchTrait` | `TestOrganizationServiceValidationOrderingAndClearing`; `TestOrganizationRepositoryMethods` |

## `members.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `MaxMemory` | `httpapi.DefaultMaxRequestBytes` | `TestMembersHandlerPostContracts` |
| `CSVPath` | removed; imports stream from multipart data and fixtures remain under `csv/` | `TestMembersHandlerPostContracts` |
| `FileFlag` | removed; uploads are never written to disk | `TestMembersHandlerPostContracts` |
| `ReadWritePermissions` | removed with upload staging | `TestMembersHandlerPostContracts` |
| `Member` | `domain.Member` | `TestMemberConstructionCloneAndValidation` |
| `MemberResponse` | `httpapi.MemberResponse` | `TestMemberServiceGetDeterministicOrderingAndNullableTrait` |
| `CreateMembersResponse` | `httpapi.CreateMembersResponse` | `TestMembersHandlerPostContracts` |
| `GetMembersResponse` | `httpapi.GetMembersResponse` | `TestMembersHandlerGetContracts` |
| `MembersHandler` | `httpapi.NewMembersHandler` | `TestMembersHandlerGetContracts`; `TestMembersHandlerPostContracts` |
| `GetMembersHandler` | GET branch of `httpapi.NewMembersHandler` | `TestMembersHandlerGetContracts` |
| `CreateMembersHandler` | POST branch of `httpapi.NewMembersHandler` | `TestMembersHandlerPostContracts`; `TestMembersHandlerRejectsMalformedCSVBeforeReconciliation` |
| `isValidFormatCSV` | consolidated into `httpapi.ParseMembersCSV` and private `normalizeMemberHeaders` | `TestParseMembersCSVRejectsInvalidInputs` |
| `createMembersFromCSV` | `httpapi.ParseMembersCSV` | `TestParseMembersCSVValidMetadataNormalizedHeadersAndHistories`; `TestParseMembersCSVRejectsInvalidInputs` |
| `saveMembersInDB` | transactional `Repository.ReconcileMembers` | `TestListAndReconcileMembers`; `TestPostgresRepositoryIntegration` |
| `getActiveMembersFromDBAsMap` | `MemberService.Get`, `Repository.ListMembers(..., true)` | `TestMemberServiceGetDeterministicOrderingAndNullableTrait`; `TestListAndReconcileMembers` |
| `getActiveMembersFromDBInPairFormat` | `Repository.ListMembers`; historical reads explicitly use `activeOnly=false` | `TestPairHistoryResolvesInactivePairsAndTriples`; `TestListHistoricalPairsIncludesInactiveMembers` |
| `GetMembersFromDB` | `Repository.ListMembers` | `TestListAndReconcileMembers`; `TestRowsErrorIsPropagated` |

## `rounds.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `NoMaxRoundErr` | removed driver-string matching; nullable aggregate handling in `Repository.AddRound` | `TestAddRoundAfterExistingMaximum`; `TestPostgresRepositoryIntegration` |
| `TimestampFormat` | private `httpapi.roundTimestampLayout`, `httpapi.ParseRoundTimestamp` | `TestParseRoundTimestampAndServiceNormalization` |
| `GetRoundsResponse` | private `httpapi.roundsResponse` | `TestRoundHandlersContracts` |
| `AddRoundHandler` | POST-add branch of `httpapi.NewRoundHandler` | `TestRoundHandlersContracts` |
| `GetRoundsHandler` | `httpapi.NewRoundsHandler` | `TestRoundHandlersContracts` |
| `RoundHandler` | `httpapi.NewRoundHandler` | `TestRoundHandlersContracts`; `TestRoundHandlersValidationAndErrorMapping` |
| `getRoundsFromDB` | `Repository.ListRounds` | `TestRoundRepositoryMethods`; `TestRowsErrorIsPropagated` |
| `RemoveRoundHandler` | DELETE branch of `httpapi.NewRoundHandler` | `TestRoundHandlersContracts` |
| `RescheduleRoundHandler` | POST-reschedule branch of `httpapi.NewRoundHandler` | `TestRoundHandlersContracts` |
| `addRound` | `RoundService.Add`, `Repository.AddRound` | `TestParseRoundTimestampAndServiceNormalization`; `TestAddRoundAfterExistingMaximum` |
| `removeRound` | `RoundService.Remove`, transactional `Repository.DeleteRound` | `TestDeleteRoundCompactsAndReconstructs`; `TestDeleteRoundNotFoundRollsBack` |
| `rescheduleRound` | `RoundService.Reschedule`, `Repository.RescheduleRound` | `TestRoundRepositoryMethods`; `TestRoundHandlersValidationAndErrorMapping` |

## `pairing.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `MaxTries` | `pairing.MaxTries` | `TestRunMaxTriesFallback` |
| `RecentRoundRange` | `pairing.RecentRoundRange` | `TestRunAvoidsRecentAndPrefersDifferentTrait` |
| `EmailIntro` | `mailer.EmailIntro` | `TestComposeGroupPreservesEnvelopeAndBody` |
| `EmailFooter` | `mailer.EmailFooter` | `TestComposeGroupPreservesEnvelopeAndBody` |
| `EmailSubject` | `mailer.EmailSubject` | `TestComposeGroupPreservesEnvelopeAndBody` |
| `Pair` | `pairing.Pair` and persistence `domain.Pair` | `TestEngineValidationAndFormatting`; `TestNewPairCanonicalizesPairsAndTriples` |
| `NewPair` | `pairing.NewPair` | `TestEngineValidationAndFormatting`; `TestRunPairOrderingAndDrawSequence` |
| `Pair.String` | `pairing.Pair.String` | `TestEngineValidationAndFormatting` |
| `Round` | `pairing.Round` and scheduled `domain.Round` | `TestEngineValidationAndFormatting`; `TestRoundConstructionAndValidation` |
| `NewRound` | `pairing.NewRound` | `TestEngineValidationAndFormatting` |
| `Round.AddPair` | `pairing.Round.AddPair` plus `AddTriple` | `TestRunPopulationPolicies`; `TestEngineValidationAndFormatting` |
| `Round.IsPaired` | `pairing.Round.IsPaired` | `TestEngineValidationAndFormatting` |
| `Round.String` | `pairing.Round.String` | `TestEngineValidationAndFormatting` |
| `MinimalMember` | `pairing.MinimalMember` | `TestRunPopulationPolicies` |
| `MembersMap` | `pairing.MembersMap` | `TestRunDeterministicAcrossMapInsertionOrder` |
| `MembersMap.UpdateLastRoundWith` | `pairing.MembersMap.UpdateLastRoundWith` | `TestRunPairOrderingAndDrawSequence` |
| `MembersMap.String` | `pairing.MembersMap.String` | `TestEngineValidationAndFormatting` |
| `RandomIntGenerator` | `pairing.RandomIntGenerator` | `TestRunPairOrderingAndDrawSequence`; `TestRunRejectsInvalidSelectorWithoutMutation` |
| `runPairingAlgorithm` | `pairing.Run` | `TestRunPopulationPolicies`; `TestRunAvoidsRecentAndPrefersDifferentTrait`; `TestRunMaxTriesFallback` |
| `runPairingRound` | `pairing.Scheduler.ProcessRound` | `TestSchedulerZeroSingletonAndTriplePolicies`; `TestSchedulerFailureReleasesWithoutPersistence` |
| `sendEmails` | delivery loop in `Scheduler.ProcessRound` | `TestSchedulerClaimsDeliversThenPersists`; `TestSchedulerFailureReleasesWithoutPersistence` |
| `selectRandomPartner` | private `pairing.selectPartner`, `selectTripleGroup` | `TestRunAvoidsRecentAndPrefersDifferentTrait`; `TestRunPairOrderingAndDrawSequence` |
| `getMinimalMembersFromDB` | `pairing.MemberLoader`/`TraitLoader` conversion in `Scheduler.ProcessRound` | `TestSchedulerClaimsDeliversThenPersists` |
| `saveRoundInDB` | transactional `Repository.PersistPairings` | `TestCompletedPairingRollsBack`; `TestPersistPairingsRejectsMissingMemberAndPartialState` |
| `sendEmail` | `mailer.ComposeGroup`, `mailer.Delivery.Deliver` | `TestComposeGroupPreservesEnvelopeAndBody`; `TestMailgunAdapterUsesInjectedAPI` |
| `runPairingScheduler` | `pairing.Scheduler.Run`, `app.CommandPair` | `TestSchedulerClaimsDeliversThenPersists`; `TestExecutePairMigrateAndGracefulServer` |

## `pairs.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `GetPairsResponsePair` | `httpapi.GetPairsResponsePair` | `TestPairHistoryResolvesInactivePairsAndTriples` |
| `GetPairsResponse` | `httpapi.GetPairsResponse` | `TestPairsHandlerContracts` |
| `GetPairsHandler` | `httpapi.NewPairsHandler` | `TestPairsHandlerContracts` |
| `getPairsFromDB` | `Repository.ListPairs` plus `PairHistoryService.ListHistory` | `TestListHistoricalPairsIncludesInactiveMembers`; `TestHistoricalMissingMemberIsIntegrityError`; `TestPairHistoryResolvesInactivePairsAndTriples` |

## `migration.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `migrateToLastRoundWithForPairing` | `Repository.ReconstructHistory`/`ReconstructOrganizationHistory`, invoked by `app.CommandMigrate` | `TestReconstructHistory`; `TestReconstructOrganizationHistoryEmpty`; `TestExecutePairMigrateAndGracefulServer`; `TestPostgresRepositoryIntegration` |

## `pairing_test.go`

| Original | Exact replacement | Direct behavior test |
| --- | --- | --- |
| `getMockMembersMap` | private deterministic `memberMap` fixture | all `internal/pairing/engine_test.go` tests |
| `TestRunPairingAlgorithmCorrectness` | direct suites for population, order, history, recency, trait preference, and fallback | `TestRunPopulationPolicies`; `TestRunPairOrderingAndDrawSequence`; `TestRunAvoidsRecentAndPrefersDifferentTrait`; `TestRunMaxTriesFallback` |
| `TestPairingAlgorithmEndToEndTest` | fake-backed scheduler tests plus gated PostgreSQL integration | `TestSchedulerClaimsDeliversThenPersists`; `TestSchedulerFailureReleasesWithoutPersistence`; `TestPostgresRepositoryIntegration` |

## Retained artifacts

The modernization retains the MIT `LICENSE`; `csv/test_john.csv`, `csv/test_john2.csv`, `csv/test_john3.csv`; `static/sample.csv`, `static/privacy.html`; `schema.sql`; all ordered `migrations/`; and the single-process `Procfile`. The obsolete `Godeps/`, committed `vendor/`, `.DS_Store`, and flat root Go implementation are deleted only after this mapping and the modular suite compile successfully.
