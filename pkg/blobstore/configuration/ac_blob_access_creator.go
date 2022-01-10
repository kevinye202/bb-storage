package configuration

import (
	"github.com/buildbarn/bb-storage/pkg/blobstore"
	"github.com/buildbarn/bb-storage/pkg/blobstore/completenesschecking"
	"github.com/buildbarn/bb-storage/pkg/blobstore/grpcclients"
	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/grpc"
	pb "github.com/buildbarn/bb-storage/pkg/proto/configuration/blobstore"
	"github.com/buildbarn/bb-storage/pkg/util"

	"google.golang.org/grpc/codes"
)

type acBlobAccessCreator struct {
	protoBlobAccessCreator

	contentAddressableStorage BlobAccessInfo
	grpcClientFactory         grpc.ClientFactory
	maximumMessageSizeBytes   int
}

// NewACBlobAccessCreator creates a BlobAccessCreator that can be
// provided to NewBlobAccessFromConfiguration() to construct a
// BlobAccess that is suitable for accessing the Action Cache.
func NewACBlobAccessCreator(contentAddressableStorage BlobAccessInfo, grpcClientFactory grpc.ClientFactory, maximumMessageSizeBytes int) BlobAccessCreator {
	return &acBlobAccessCreator{
		contentAddressableStorage: contentAddressableStorage,
		grpcClientFactory:         grpcClientFactory,
		maximumMessageSizeBytes:   maximumMessageSizeBytes,
	}
}

func (bac *acBlobAccessCreator) GetReadBufferFactory() blobstore.ReadBufferFactory {
	return blobstore.ACReadBufferFactory
}

func (bac *acBlobAccessCreator) GetStorageTypeName() string {
	return "ac"
}

func (bac *acBlobAccessCreator) NewCustomBlobAccess(configuration *pb.BlobAccessConfiguration) (BlobAccessInfo, string, error) {
	switch backend := configuration.Backend.(type) {
	case *pb.BlobAccessConfiguration_ActionResultExpiring:
		base, err := NewNestedBlobAccess(backend.ActionResultExpiring.Backend, bac)
		if err != nil {
			return BlobAccessInfo{}, "", err
		}
		minimumValidity := backend.ActionResultExpiring.MinimumValidity
		if err := minimumValidity.CheckValid(); err != nil {
			return BlobAccessInfo{}, "", util.StatusWrapWithCode(err, codes.InvalidArgument, "Invalid minimum validity")
		}
		maximumValidityJitter := backend.ActionResultExpiring.MaximumValidityJitter
		if err := maximumValidityJitter.CheckValid(); err != nil {
			return BlobAccessInfo{}, "", util.StatusWrapWithCode(err, codes.InvalidArgument, "Invalid maximum validity jitter")
		}
		return BlobAccessInfo{
			BlobAccess: blobstore.NewActionResultExpiringBlobAccess(
				base.BlobAccess,
				clock.SystemClock,
				bac.maximumMessageSizeBytes,
				minimumValidity.AsDuration(),
				maximumValidityJitter.AsDuration()),
			DigestKeyFormat: base.DigestKeyFormat,
		}, "completeness_checking", nil
	case *pb.BlobAccessConfiguration_CompletenessChecking:
		base, err := NewNestedBlobAccess(backend.CompletenessChecking, bac)
		if err != nil {
			return BlobAccessInfo{}, "", err
		}
		return BlobAccessInfo{
			BlobAccess: completenesschecking.NewCompletenessCheckingBlobAccess(
				base.BlobAccess,
				bac.contentAddressableStorage.BlobAccess,
				blobstore.RecommendedFindMissingDigestsCount,
				bac.maximumMessageSizeBytes),
			DigestKeyFormat: base.DigestKeyFormat.Combine(bac.contentAddressableStorage.DigestKeyFormat),
		}, "completeness_checking", nil
	case *pb.BlobAccessConfiguration_Grpc:
		client, err := bac.grpcClientFactory.NewClientFromConfiguration(backend.Grpc)
		if err != nil {
			return BlobAccessInfo{}, "", err
		}
		return BlobAccessInfo{
			BlobAccess:      grpcclients.NewACBlobAccess(client, bac.maximumMessageSizeBytes),
			DigestKeyFormat: digest.KeyWithInstance,
		}, "grpc", nil
	default:
		return newProtoCustomBlobAccess(bac, configuration)
	}
}
