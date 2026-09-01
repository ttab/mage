package libschema

// Exported for the tests, so that vendoring can be driven against a fixture
// library on disk rather than against a real module in the cache.
var (
	CheckWith  = check
	VendorWith = vendor
)

// TernSeparator lets a test build a migration the flattener will accept.
const TernSeparator = ternSeparator
