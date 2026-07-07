package baseapp

import (
	"context"
	"fmt"
	"strconv"
	"time"

	gogogrpc "github.com/cosmos/gogoproto/grpc"
	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	errorsmod "cosmossdk.io/errors"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"
)

// RegisterGRPCServer registers gRPC services directly with the gRPC server.
func (app *BaseApp) RegisterGRPCServer(server gogogrpc.Server) {
	app.RegisterGRPCServerWithSkipCheckHeader(server, false)
}

// RegisterGRPCServerWithSkipCheckHeader registers gRPC services with the specified gRPC server
// and bypass check header flag. During the commit phase, gRPC queries may be processed before the block header
// is fully updated, causing header checks to fail erroneously. Skipping the header check in these cases prevents
// false negatives and ensures more robust query handling.  While bypassing the header check is generally preferred to avoid false
// negatives during the commit phase, there are niche scenarios where someone might want to enable it.
// For instance, if an application requires strict validation to ensure that the query context exactly
// reflects the expected block header (for consistency or security reasons), then enabling header checks
// could be beneficial. However, this strictness comes at the cost of potentially more frequent errors
// when queries occur during the commit phase.
func (app *BaseApp) RegisterGRPCServerWithSkipCheckHeader(server gogogrpc.Server, skipCheckHeader bool) {
	// Define an interceptor for all gRPC queries: this interceptor will create
	// a new sdk.Context, and pass it into the query handler.
	interceptor := func(grpcCtx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		// Gonka: query telemetry. Two layers:
		//  - go-metrics counters (`query_count`, `query_<method>`) preserved
		//    for backward compatibility with the existing telemetry series.
		//  - gonka_query_* Prometheus metrics with method/status/transport
		//    labels, registered in query_metrics.go. Configured slow queries
		//    also emit a structured log line carrying request summary + peer.
		method := info.FullMethod
		transport := GonkaTransportGRPC

		// If there's some metadata in the context, retrieve it.
		md, ok := metadata.FromIncomingContext(grpcCtx)
		if !ok {
			return nil, status.Error(codes.Internal, "unable to retrieve metadata")
		}

		// Gonka: REST traffic flows through grpc-gateway and arrives here as a
		// gRPC call carrying `grpcgateway-*` forwarded headers.
		if len(md.Get("grpcgateway-user-agent")) > 0 {
			transport = GonkaTransportREST
		}

		// Gonka: peer attribution (bucketed by /24 or /48 to bound cardinality).
		peerLabel := GonkaPeerUnknown
		if p, pok := peer.FromContext(grpcCtx); pok && p.Addr != nil {
			peerLabel = gonkaSanitizePeer(p.Addr.String())
		}

		telemetry.IncrCounter(1, "query", "count")
		telemetry.IncrCounter(1, "query", method)
		defer telemetry.MeasureSince(telemetry.Now(), method)

		gonkaQueryInFlight.WithLabelValues(method, transport).Inc()
		defer gonkaQueryInFlight.WithLabelValues(method, transport).Dec()

		var (
			sdkCtx          sdk.Context
			sdkCtxValid     bool
			requestedHeight int64
			start           = time.Now()
			setupDuration   time.Duration
			handlerStart    time.Time
			handlerDuration time.Duration
		)

		// Gonka: deferred metrics observation. Added before the recovery defer
		// below so it pops second (LIFO) and observes post-recovery err.
		defer func() {
			totalDuration := time.Since(start)
			requestSize := -1
			if s, ok := req.(interface{ Size() int }); ok {
				requestSize = s.Size()
			}
			rec := gonkaQueryRecord{
				method:          method,
				transport:       transport,
				status:          gonkaClassifyGRPCErr(err),
				peer:            peerLabel,
				start:           start,
				setupDuration:   setupDuration,
				handlerDuration: handlerDuration,
				totalDuration:   totalDuration,
				respBytes:       -1,
				requestedHeight: requestedHeight,
				currentHeight:   app.LastBlockHeight(),
				requestSummary: func(includeContent bool) string {
					if includeContent {
						return fmt.Sprintf("%v", req)
					}
					return fmt.Sprintf("type=%T size=%dB", req, requestSize)
				},
			}
			if s, sok := resp.(interface{ Size() int }); sok {
				rec.respBytes = s.Size()
			}
			if sdkCtxValid {
				if gm := sdkCtx.GasMeter(); gm != nil {
					rec.gasConsumed = gm.GasConsumed()
				}
			}
			gonkaObserve(rec)
			gonkaMaybeSlowLog(app.logger, rec)
		}()

		// Get height header from the request context, if present.
		var height int64
		if heightHeaders := md.Get(grpctypes.GRPCBlockHeightHeader); len(heightHeaders) == 1 {
			height, err = strconv.ParseInt(heightHeaders[0], 10, 64)
			if err != nil {
				return nil, errorsmod.Wrapf(
					sdkerrors.ErrInvalidRequest,
					"Baseapp.RegisterGRPCServer: invalid height header %q: %v", grpctypes.GRPCBlockHeightHeader, err)
			}
			if err := checkNegativeHeight(height); err != nil {
				return nil, err
			}
		}
		requestedHeight = height

		// Gonka: time the sdk.Context construction. Commit-phase blocking
		// shows up here, not in the handler.
		setupStart := time.Now()
		sdkCtx, err = app.CreateQueryContextWithCheckHeader(height, false, !skipCheckHeader)
		setupDuration = time.Since(setupStart)
		if err != nil {
			return nil, err
		}
		sdkCtxValid = true

		// Add relevant gRPC headers
		if height == 0 {
			height = sdkCtx.BlockHeight() // If height was not set in the request, set it to the latest
		}

		// Attach the sdk.Context into the gRPC's context.Context.
		grpcCtx = context.WithValue(grpcCtx, sdk.SdkContextKey, sdkCtx)

		md = metadata.Pairs(grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(height, 10))
		if err = grpc.SetHeader(grpcCtx, md); err != nil {
			app.logger.Error("failed to set gRPC header", "err", err)
		}

		app.logger.Debug("gRPC query received", "type", fmt.Sprintf("%#v", req))

		// Catch an OutOfGasPanic caused in the query handlers
		defer func() {
			if r := recover(); r != nil {
				switch rType := r.(type) {
				case storetypes.ErrorOutOfGas:
					err = errorsmod.Wrapf(sdkerrors.ErrOutOfGas, "Query gas limit exceeded: %v, out of gas in location: %v", sdkCtx.GasMeter().Limit(), rType.Descriptor)
				default:
					panic(r)
				}
			}
		}()

		handlerStart = time.Now()
		resp, err = handler(grpcCtx, req)
		handlerDuration = time.Since(handlerStart)
		return resp, err
	}

	// Loop through all services and methods, add the interceptor, and register
	// the service.
	for _, data := range app.GRPCQueryRouter().serviceData {
		desc := data.serviceDesc
		newMethods := make([]grpc.MethodDesc, len(desc.Methods))

		for i, method := range desc.Methods {
			methodHandler := method.Handler
			newMethods[i] = grpc.MethodDesc{
				MethodName: method.MethodName,
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					return methodHandler(srv, ctx, dec, grpcmiddleware.ChainUnaryServer(
						grpcrecovery.UnaryServerInterceptor(),
						interceptor,
					))
				},
			}
		}

		newDesc := &grpc.ServiceDesc{
			ServiceName: desc.ServiceName,
			HandlerType: desc.HandlerType,
			Methods:     newMethods,
			Streams:     desc.Streams,
			Metadata:    desc.Metadata,
		}

		server.RegisterService(newDesc, data.handler)
	}
}
