// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package validateonly implements AIP-163 change validation: a mutation
// whose request sets validate_only runs every check of the live request
// (permissions, validation, AIP-154 preconditions) and returns the response
// the live request would return, without the write or its side effects.
//
// Run splits a mutation into a validate step, which performs every check and
// builds the would-be response, and a commit step, which writes; with
// validate_only set the commit step is skipped. Requested reads the
// validate_only flag of a request by descriptor, refusing a field of the
// wrong kind. OmitGenerated clears the OUTPUT_ONLY values a validation
// response cannot produce truthfully. update.Patch takes the flag through
// update.WithValidateOnly.
//
// # Usage
//
//	func (s *Server) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.Book, error) {
//		return validateonly.Run(ctx, req.GetValidateOnly(),
//			func(ctx context.Context) (*pb.Book, error) {
//				if err := s.authorize(ctx, req); err != nil {
//					return nil, err
//				}
//				book, err := s.prepare(req.GetBook()) // validate, fill defaults
//				if err != nil {
//					return nil, err
//				}
//				return book, nil
//			},
//			func(ctx context.Context, book *pb.Book) (*pb.Book, error) {
//				return s.store.Create(ctx, book)
//			})
//	}
//
// See README.md for the complete guide and the AIP-163 conformance table.
package validateonly
