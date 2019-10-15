package pail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createS3Client(region string) (*s3.S3, error) {
	sess, err := session.NewSession(&aws.Config{Region: aws.String(region)})
	if err != nil {
		return nil, errors.Wrap(err, "problem connecting to AWS")
	}
	svc := s3.New(sess)
	return svc, nil
}

func cleanUpS3Bucket(name, prefix, region string) error {
	svc, err := createS3Client(region)
	if err != nil {
		return errors.Wrap(err, "clean up failed")
	}
	deleteObjectsInput := &s3.DeleteObjectsInput{
		Bucket: aws.String(name),
		Delete: &s3.Delete{},
	}
	listInput := &s3.ListObjectsInput{
		Bucket: aws.String(name),
		Prefix: aws.String(prefix),
	}
	var result *s3.ListObjectsOutput

	for {
		result, err = svc.ListObjects(listInput)
		if err != nil {
			return errors.Wrap(err, "clean up failed")
		}

		for _, object := range result.Contents {
			deleteObjectsInput.Delete.Objects = append(deleteObjectsInput.Delete.Objects, &s3.ObjectIdentifier{
				Key: object.Key,
			})
		}

		if deleteObjectsInput.Delete.Objects != nil {
			_, err = svc.DeleteObjects(deleteObjectsInput)
			if err != nil {
				return errors.Wrap(err, "failed to delete S3 bucket")
			}
			deleteObjectsInput.Delete = &s3.Delete{}
		}

		if *result.IsTruncated {
			listInput.Marker = result.Contents[len(result.Contents)-1].Key
		} else {
			break
		}
	}

	return nil
}

func getS3SmallBucketTests(ctx context.Context, tempdir, s3BucketName, s3Prefix, s3Region string) []bucketTestCase {
	return []bucketTestCase{
		{
			id: "VerifyBucketType",
			test: func(t *testing.T, b Bucket) {
				bucket, ok := b.(*s3BucketSmall)
				require.True(t, ok)
				assert.NotNil(t, bucket)
			},
		},
		{
			id: "TestCredentialsOverrideDefaults",
			test: func(t *testing.T, b Bucket) {
				input := &s3.GetBucketLocationInput{
					Bucket: aws.String(s3BucketName),
				}

				rawBucket := b.(*s3BucketSmall)
				_, err := rawBucket.svc.GetBucketLocationWithContext(ctx, input)
				assert.NoError(t, err)

				badOptions := S3Options{
					Credentials: CreateAWSCredentials("asdf", "asdf", "asdf"),
					Region:      s3Region,
					Name:        s3BucketName,
				}
				badBucket, err := NewS3Bucket(badOptions)
				require.NoError(t, err)
				rawBucket = badBucket.(*s3BucketSmall)
				_, err = rawBucket.svc.GetBucketLocationWithContext(ctx, input)
				assert.Error(t, err)
			},
		},
		{
			id: "TestCheckPassesWhenDoNotHaveAccess",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				rawBucket.name = "mciuploads"
				assert.NoError(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestCheckFailsWhenBucketDNE",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				rawBucket.name = newUUID()
				assert.Error(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsOption",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				newFile, err := os.Create(filepath.Join(tempdir, "creds"))
				require.NoError(t, err)
				defer newFile.Close()
				_, err = newFile.WriteString("[my_profile]\n")
				require.NoError(t, err)
				awsKey := fmt.Sprintf("aws_access_key_id = %s\n", os.Getenv("AWS_KEY"))
				_, err = newFile.WriteString(awsKey)
				require.NoError(t, err)
				awsSecret := fmt.Sprintf("aws_secret_access_key = %s\n", os.Getenv("AWS_SECRET"))
				_, err = newFile.WriteString(awsSecret)
				require.NoError(t, err)

				sharedCredsOptions := S3Options{
					SharedCredentialsFilepath: filepath.Join(tempdir, "creds"),
					SharedCredentialsProfile:  "my_profile",
					Region:                    s3Region,
					Name:                      s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsUsesCorrectDefaultFile",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "default",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(sharedCredsOptions)
				require.NoError(t, err)
				homeDir, err := homedir.Dir()
				require.NoError(t, err)
				fileName := filepath.Join(homeDir, ".aws", "credentials")
				_, err = os.Stat(fileName)
				if err == nil {
					assert.NoError(t, sharedCredsBucket.Check(ctx))
				} else {
					assert.True(t, os.IsNotExist(err))
				}
			},
		},
		{
			id: "TestSharedCredentialsFailsWhenProfileDNE",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "DNE",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				_, err := NewS3Bucket(sharedCredsOptions)
				assert.Error(t, err)
			},
		},

		{
			id: "TestPermissions",
			test: func(t *testing.T, b Bucket) {
				// default permissions
				key1 := newUUID()
				writer, err := b.Writer(ctx, key1)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketSmall)
				objectACLInput := &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key1)),
				}
				objectACLOutput, err := rawBucket.svc.GetObjectAcl(objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 1, len(objectACLOutput.Grants))
				assert.Equal(t, "FULL_CONTROL", *objectACLOutput.Grants[0].Permission)

				// explicitly set permissions
				openOptions := S3Options{
					Region:     s3Region,
					Name:       s3BucketName,
					Prefix:     s3Prefix + newUUID(),
					Permission: "public-read",
				}
				openBucket, err := NewS3Bucket(openOptions)
				require.NoError(t, err)
				key2 := newUUID()
				writer, err = openBucket.Writer(ctx, key2)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = openBucket.(*s3BucketSmall)
				objectACLInput = &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key2)),
				}
				objectACLOutput, err = rawBucket.svc.GetObjectAcl(objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, "READ", *objectACLOutput.Grants[1].Permission)

				// copy with permissions
				destKey := newUUID()
				copyOpts := CopyOptions{
					SourceKey:         key1,
					DestinationKey:    destKey,
					DestinationBucket: openBucket,
				}
				require.NoError(t, b.Copy(ctx, copyOpts))
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, "READ", *objectACLOutput.Grants[1].Permission)
			},
		},
		{
			id: "TestContentType",
			test: func(t *testing.T, b Bucket) {
				// default content type
				key := newUUID()
				writer, err := b.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketSmall)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(getObjectInput)
				require.NoError(t, err)
				assert.Nil(t, getObjectOutput.ContentType)

				// explicitly set content type
				htmlOptions := S3Options{
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + newUUID(),
					ContentType: "html/text",
				}
				htmlBucket, err := NewS3Bucket(htmlOptions)
				require.NoError(t, err)
				key = newUUID()
				writer, err = htmlBucket.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = htmlBucket.(*s3BucketSmall)
				getObjectInput = &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err = rawBucket.svc.GetObject(getObjectInput)
				require.NoError(t, err)
				require.NotNil(t, getObjectOutput.ContentType)
				assert.Equal(t, "html/text", *getObjectOutput.ContentType)
			},
		},
	}
}

func getS3LargeBucketTests(ctx context.Context, tempdir, s3BucketName, s3Prefix, s3Region string) []bucketTestCase {
	return []bucketTestCase{
		{
			id: "VerifyBucketType",
			test: func(t *testing.T, b Bucket) {
				bucket, ok := b.(*s3BucketLarge)
				require.True(t, ok)
				assert.NotNil(t, bucket)
			},
		},
		{
			id: "TestCredentialsOverrideDefaults",
			test: func(t *testing.T, b Bucket) {
				input := &s3.GetBucketLocationInput{
					Bucket: aws.String(s3BucketName),
				}

				rawBucket := b.(*s3BucketLarge)
				_, err := rawBucket.svc.GetBucketLocationWithContext(ctx, input)
				assert.NoError(t, err)

				badOptions := S3Options{
					Credentials: CreateAWSCredentials("asdf", "asdf", "asdf"),
					Region:      s3Region,
					Name:        s3BucketName,
				}
				badBucket, err := NewS3MultiPartBucket(badOptions)
				require.NoError(t, err)
				rawBucket = badBucket.(*s3BucketLarge)
				_, err = rawBucket.svc.GetBucketLocationWithContext(ctx, input)
				assert.Error(t, err)
			},
		},
		{
			id: "TestCheckPassesWhenDoNotHaveAccess",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketLarge)
				rawBucket.name = "mciuploads"
				assert.NoError(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestCheckFailsWhenBucketDNE",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketLarge)
				rawBucket.name = newUUID()
				assert.Error(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsOption",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				newFile, err := os.Create(filepath.Join(tempdir, "creds"))
				require.NoError(t, err)
				defer newFile.Close()
				_, err = newFile.WriteString("[my_profile]\n")
				require.NoError(t, err)
				awsKey := fmt.Sprintf("aws_access_key_id = %s\n", os.Getenv("AWS_KEY"))
				_, err = newFile.WriteString(awsKey)
				require.NoError(t, err)
				awsSecret := fmt.Sprintf("aws_secret_access_key = %s\n", os.Getenv("AWS_SECRET"))
				_, err = newFile.WriteString(awsSecret)
				require.NoError(t, err)

				sharedCredsOptions := S3Options{
					SharedCredentialsFilepath: filepath.Join(tempdir, "creds"),
					SharedCredentialsProfile:  "my_profile",
					Region:                    s3Region,
					Name:                      s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsUsesCorrectDefaultFile",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "default",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(sharedCredsOptions)
				require.NoError(t, err)
				homeDir, err := homedir.Dir()
				require.NoError(t, err)
				fileName := filepath.Join(homeDir, ".aws", "credentials")
				_, err = os.Stat(fileName)
				if err == nil {
					assert.NoError(t, sharedCredsBucket.Check(ctx))
				} else {
					assert.True(t, os.IsNotExist(err))
				}
			},
		},
		{
			id: "TestSharedCredentialsFailsWhenProfileDNE",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "DNE",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				_, err := NewS3Bucket(sharedCredsOptions)
				assert.Error(t, err)
			},
		},

		{
			id: "TestPermissions",
			test: func(t *testing.T, b Bucket) {
				// default permissions
				key1 := newUUID()
				writer, err := b.Writer(ctx, key1)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketLarge)
				objectACLInput := &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key1)),
				}
				objectACLOutput, err := rawBucket.svc.GetObjectAcl(objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 1, len(objectACLOutput.Grants))
				assert.Equal(t, "FULL_CONTROL", *objectACLOutput.Grants[0].Permission)

				// explicitly set permissions
				openOptions := S3Options{
					Region:     s3Region,
					Name:       s3BucketName,
					Prefix:     s3Prefix + newUUID(),
					Permission: "public-read",
				}
				openBucket, err := NewS3MultiPartBucket(openOptions)
				require.NoError(t, err)
				key2 := newUUID()
				writer, err = openBucket.Writer(ctx, key2)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = openBucket.(*s3BucketLarge)
				objectACLInput = &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key2)),
				}
				objectACLOutput, err = rawBucket.svc.GetObjectAcl(objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, "READ", *objectACLOutput.Grants[1].Permission)

				// copy with permissions
				destKey := newUUID()
				copyOpts := CopyOptions{
					SourceKey:         key1,
					DestinationKey:    destKey,
					DestinationBucket: openBucket,
				}
				require.NoError(t, b.Copy(ctx, copyOpts))
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, "READ", *objectACLOutput.Grants[1].Permission)
			},
		},
		{
			id: "TestContentType",
			test: func(t *testing.T, b Bucket) {
				// default content type
				key := newUUID()
				writer, err := b.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketLarge)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(getObjectInput)
				require.NoError(t, err)
				assert.Nil(t, getObjectOutput.ContentType)

				// explicitly set content type
				htmlOptions := S3Options{
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + newUUID(),
					ContentType: "html/text",
				}
				htmlBucket, err := NewS3MultiPartBucket(htmlOptions)
				require.NoError(t, err)
				key = newUUID()
				writer, err = htmlBucket.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = htmlBucket.(*s3BucketLarge)
				getObjectInput = &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err = rawBucket.svc.GetObject(getObjectInput)
				require.NoError(t, err)
				require.NotNil(t, getObjectOutput.ContentType)
				assert.Equal(t, "html/text", *getObjectOutput.ContentType)
			},
		},
	}
}