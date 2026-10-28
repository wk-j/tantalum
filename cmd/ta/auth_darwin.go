package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework LocalAuthentication
#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>
#include <string.h>

// ta_confirm_user shows the system prompt (Touch ID, Apple Watch or the login
// password) and blocks until the person answers. It returns NULL when they
// approve, otherwise a malloc'd message the caller frees.
static char *ta_confirm_user(const char *reason) {
	@autoreleasepool {
		LAContext *context = [[LAContext alloc] init];
		NSError *error = nil;
		if (![context canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&error]) {
			return strdup(error.localizedDescription.UTF8String);
		}
		dispatch_semaphore_t answered = dispatch_semaphore_create(0);
		__block NSString *refusal = nil;
		[context evaluatePolicy:LAPolicyDeviceOwnerAuthentication
		        localizedReason:[NSString stringWithUTF8String:reason]
		                  reply:^(BOOL success, NSError *err) {
			if (!success) {
				refusal = err.localizedDescription ?: @"not approved";
			}
			dispatch_semaphore_signal(answered);
		}];
		dispatch_semaphore_wait(answered, DISPATCH_TIME_FOREVER);
		return refusal ? strdup(refusal.UTF8String) : NULL;
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// confirmUserPresence asks the person at this Mac to approve with Touch ID, or
// with an Apple Watch or the login password where Touch ID is not available.
// reason completes the prompt's sentence "ta is trying to …". An agent cannot
// answer the prompt: it needs a finger, a watch or the password typed into a
// system dialog.
func confirmUserPresence(reason string) error {
	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))
	if refusal := C.ta_confirm_user(cReason); refusal != nil {
		defer C.free(unsafe.Pointer(refusal))
		return errors.New(C.GoString(refusal))
	}
	return nil
}
