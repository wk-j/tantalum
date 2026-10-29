package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static NSPasteboard *ta_pasteboard(const char *name) {
	if (name[0] == 0) {
		return [NSPasteboard generalPasteboard];
	}
	return [NSPasteboard pasteboardWithName:[NSString stringWithUTF8String:name]];
}

// ta_clipboard_set puts text on the pasteboard, marked with the
// nspasteboard.org concealed type so clipboard managers that honour it keep no
// history of it, and returns the pasteboard's change count, or -1 when text is
// not UTF-8.
static long ta_clipboard_set(const char *name, const void *text, long length) {
	@autoreleasepool {
		NSString *s = [[NSString alloc] initWithBytes:text length:length encoding:NSUTF8StringEncoding];
		if (s == nil) {
			return -1;
		}
		NSPasteboard *pb = ta_pasteboard(name);
		[pb declareTypes:@[NSPasteboardTypeString, @"org.nspasteboard.ConcealedType"] owner:nil];
		[pb setString:s forType:NSPasteboardTypeString];
		[pb setString:@"" forType:@"org.nspasteboard.ConcealedType"];
		return pb.changeCount;
	}
}

// ta_clipboard_clear_if empties the pasteboard when its change count is still
// count, that is when nothing was copied since.
static int ta_clipboard_clear_if(const char *name, long count) {
	@autoreleasepool {
		NSPasteboard *pb = ta_pasteboard(name);
		if (pb.changeCount != count) {
			return 0;
		}
		[pb clearContents];
		return 1;
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// pasteboardName is the pasteboard revealSecret copies to; "" is the general
// clipboard. Tests use another one so they leave the clipboard alone.
var pasteboardName = ""

// copyToClipboard puts text on the clipboard and returns the change count to
// hand to clearClipboardIfUnchanged.
func copyToClipboard(text string) (int, error) {
	name := C.CString(pasteboardName)
	defer C.free(unsafe.Pointer(name))
	data := C.CBytes([]byte(text))
	defer C.free(data)
	count := C.ta_clipboard_set(name, data, C.long(len(text)))
	if count < 0 {
		return 0, errors.New("the value is not UTF-8 text, so it cannot go on the clipboard")
	}
	return int(count), nil
}

// clearClipboardIfUnchanged empties the clipboard unless something was copied
// after the copy that returned changeCount, and reports whether it did.
func clearClipboardIfUnchanged(changeCount int) bool {
	name := C.CString(pasteboardName)
	defer C.free(unsafe.Pointer(name))
	return C.ta_clipboard_clear_if(name, C.long(changeCount)) == 1
}
