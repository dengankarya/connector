// Package account provides the unified merchant account ledger view.
// It merges payment transactions, shipping balance topups, and shipping holds
// into a single activity feed accessible at GET /accounts/transactions.
package account

import (
	"context"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/google/uuid"
)

var (
	ErrNotFound               = common.NewDomainError("NF_TRANSACTION_NOT_FOUND", "transaction not found")
	ErrInsufficientBalance    = common.ErrInsufficientBalance // PR_INSUFFICIENT_BALANCE
	ErrHoldNotFound           = common.NewDomainError("NF_HOLD_NOT_FOUND", "shipping hold not found")
	ErrHoldAlreadyActioned    = common.NewDomainError("CF_HOLD_ALREADY_ACTIONED", "shipping hold already confirmed or released")
	ErrDuplicateHold          = common.NewDomainError("CF_DUPLICATE_HOLD", "an active hold already exists for this order")
	ErrInvalidCursor          = common.NewDomainError("BR_INVALID_CURSOR", "invalid pagination cursor")
	ErrGatewayAccountNotFound = common.NewDomainError("NF_GATEWAY_ACCOUNT_NOT_FOUND", "no gateway account found for this tenant")
	ErrGatewayAccountExists   = common.NewDomainError("CF_GATEWAY_ACCOUNT_EXISTS", "a gateway account already exists for this tenant")
	ErrGatewayNotConfigured   = common.NewDomainError("SV_GATEWAY_NOT_CONFIGURED", "payment gateway is not configured")
)

// TransactionFilter controls cursor-paginated listing of account activity.
type TransactionFilter struct {
	// Limit is the maximum number of items per page (default 20, max 100).
	Limit int
	// Cursor is the opaque token returned by the previous response; empty for the first page.
	Cursor string
	// Types restricts results to the given activity types; empty means all types.
	Types []ActivityType
	// From, when non-nil, restricts to items created at or after this time (inclusive).
	From *time.Time
	// To, when non-nil, restricts to items created before this time (exclusive).
	To *time.Time
}

// ActivityType classifies a single entry in the unified activity feed.
type ActivityType string

const (
	ActivityPayment                 ActivityType = "payment"                   // customer payment session
	ActivityBalanceTopup            ActivityType = "balance_topup"             // manual top-up by platform operator
	ActivityShipmentHold            ActivityType = "shipment_hold"             // funds reserved for a draft order
	ActivityShipmentConfirmed       ActivityType = "shipment_confirmed"        // shipment confirmed, funds disbursed
	ActivityShipmentReleased        ActivityType = "shipment_released"         // order cancelled, funds returned
	ActivityShipmentPriceAdjustment ActivityType = "shipment_price_adjustment" // actual weight differed from estimate
)

// ActivityItem is a single entry in the unified merchant activity feed.
// Read-only projection across payment_transactions, shipping_topups, and shipping_holds.
type ActivityItem struct {
	ID          uuid.UUID    `json:"id"`
	Type        ActivityType `json:"type"`
	Amount      int64        `json:"amount"`
	Currency    string       `json:"currency"`
	OrderNumber string       `json:"order_number,omitempty"`
	Status      string       `json:"status,omitempty"` // payment status or hold status
	Note        string       `json:"note,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// LedgerEntry is a simplified projection of payment_ledger_entries for the detail view.
type LedgerEntry struct {
	AccountType string    `json:"account_type"`
	Direction   string    `json:"direction"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// ActivityDetail is the full detail for a single activity item.
// LedgerEntries and Metadata are populated for payment type items only.
type ActivityDetail struct {
	ActivityItem
	Metadata      map[string]any `json:"metadata,omitempty"`
	LedgerEntries []LedgerEntry  `json:"ledger_entries,omitempty"`
}

// ─── Shipping balance types ───────────────────────────────────────────────────

// ShippingBalance is the snapshot of a merchant's shipping wallet.
type ShippingBalance struct {
	ID        uuid.UUID `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Available int64     `json:"available"`
	OnHold    int64     `json:"on_hold"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HoldStatus is the lifecycle state of a shipping hold.
type HoldStatus string

const (
	HoldStatusHolding   HoldStatus = "holding"
	HoldStatusConfirmed HoldStatus = "confirmed"
	HoldStatusReleased  HoldStatus = "released"
)

// ShippingHold reserves funds for a single draft order.
type ShippingHold struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    int64      `json:"tenant_id"`
	OrderNumber string     `json:"order_number"`
	Amount      int64      `json:"amount"`
	Currency    string     `json:"currency"`
	Status      HoldStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
}

// ShippingPriceAdjustment records a single shipping cost correction for an order.
// Created when the Biteship order.price webhook fires with a price different from the estimate.
type ShippingPriceAdjustment struct {
	ID          uuid.UUID `json:"id"`
	TenantID    int64     `json:"tenant_id"`
	OrderNumber string    `json:"order_number"`
	OldPrice    int64     `json:"old_price"`
	NewPrice    int64     `json:"new_price"`
	// Diff = NewPrice - OldPrice. Positive means balance was debited; negative means credited.
	Diff      int64     `json:"diff"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
}

// ShippingTopup records a single manual top-up by the platform operator.
type ShippingTopup struct {
	ID        uuid.UUID `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── Payment balance ──────────────────────────────────────────────────────────

// MerchantPaymentBalance is the transaction-derived balance for a merchant's
// payment settlements. All figures are computed from payment_transactions and
// payment_payouts in our DB — no real-time gateway API call is required.
type MerchantPaymentBalance struct {
	TenantID int64 `json:"tenant_id"`

	// Settled is the total merchant_amount across all 'settled' transactions.
	Settled int64 `json:"settled"`

	// PendingSettlement is merchant_amount across 'paid' transactions not yet
	// marked settled (typically T+1 or T+2 depending on the gateway).
	PendingSettlement int64 `json:"pending_settlement"`

	// PaidOut is the total amount already disbursed to the merchant via
	// completed payouts from the platform account.
	PaidOut int64 `json:"paid_out"`

	// AvailableToPayout is Settled minus PaidOut — the amount the platform
	// can still disburse to the merchant.
	AvailableToPayout int64 `json:"available_to_payout"`

	Currency string `json:"currency"`
}

// UnifiedBalance is the combined merchant wallet view returned by GET /accounts/balance.
// It merges the shipping wallet (available/on-hold) with the transaction-derived
// payment settlement balance (settled/pending/paid-out), and the live gateway sub-account balance.
type UnifiedBalance struct {
	// Shipping wallet — available funds and funds on hold for pending shipments.
	Shipping *ShippingBalance `json:"shipping"`
	// Payment settlement — computed from payment_transactions and payment_payouts.
	Payment *MerchantPaymentBalance `json:"payment"`
	// Gateway is the real-time balance from the payment gateway sub-account.
	// Nil when no gateway sub-account is configured for this tenant.
	Gateway *GatewayBalance `json:"gateway,omitempty"`
}

// ─── Gateway account types ────────────────────────────────────────────────────

// GatewayAccount is the stored record of a merchant's payment gateway sub-account.
type GatewayAccount struct {
	ID               uuid.UUID `json:"id"`
	TenantID         int64     `json:"tenant_id"`
	Gateway          string    `json:"gateway"`
	GatewayAccountID string    `json:"gateway_account_id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GatewayBalance is the real-time balance reported by the payment gateway sub-account.
type GatewayBalance struct {
	GatewayAccountID string `json:"gateway_account_id"`
	Pending          int64  `json:"pending"`
	Available        int64  `json:"available"`
	Currency         string `json:"currency"`
}

// GatewayClient is the interface for payment gateway sub-account operations.
// Implemented structurally by *doku.Client.
type GatewayClient interface {
	CreateSubAccount(ctx context.Context, email, name string) (gatewayAccountID, status string, err error)
	GetBalance(ctx context.Context, gatewayAccountID string) (pending, available int64, err error)
	SendPayout(ctx context.Context, gatewayAccountID string, amount int64, invoiceNumber, bankCode, bankAccountNumber, bankAccountName string) (status string, err error)
}

// XenditGatewayClient extends GatewayClient with Xendit-specific account management.
// Implemented structurally by *xendit.Client.
type XenditGatewayClient interface {
	GatewayClient
	// GetAccount fetches the current account status and public profile from Xendit.
	GetAccount(ctx context.Context, accountID string) (*XenditAccountInfo, error)
	// CreateAccountHolder submits KYC business details for a sub-account.
	// Returns the Xendit account_holder_id on success.
	CreateAccountHolder(ctx context.Context, subAccountID string, req CreateAccountHolderRequest) (accountHolderID string, err error)
	// LinkAccountHolder links an account holder to a sub-account via PATCH /v2/accounts/{id}.
	// This must be called after CreateAccountHolder to begin the verification flow.
	LinkAccountHolder(ctx context.Context, subAccountID, accountHolderID string) error
}

// validIndustryCategories is the exhaustive set of industry_category values accepted by Xendit.
var validIndustryCategories = map[string]struct{}{
	"ACCOUNTING_AUDITING_AND_BOOKKEEPING_SERVICES": {}, "ADVERTISING_SERVICES": {}, "AGRICULTURAL_COOPERATIVES": {},
	"ALCOHOLIC_BEVERAGE_WHOLESALERS": {}, "AMUSEMENT_PARKS_CIRCUSES_CARNIVALS_AND_FORTUNE_TELLERS": {}, "ANTIQUE_REPRODUCTION_SHOPS": {},
	"ARCHITECTURAL_ENGINEERING_AND_SURVEYING_SERVICES": {}, "ART_AND_CRAFTS_NEW": {}, "ASSET_MANAGEMENT_COMPANY_NEW": {},
	"AUCTION": {}, "AUTOMATED_FUEL_DISPENSERS": {}, "AUTOMOBILE_RENTALS": {},
	"AUTOMOTIVE_BODY_REPAIR_SHOPS": {}, "AUTOMOTIVE_CAR_MOTORCYCLE_BOATS_AND_MISCELLANEOUS_AUTOMOTIVE_EQUIPMENT_DEALER": {}, "AUTOMOTIVE_PARTS_AND_ACCESSORIES_OUTLETS": {},
	"AUTOMOTIVE_REPAIR_WASHES_AND_TOWING_SERVICE": {}, "BAKERIES": {}, "BANKS": {},
	"BEAUTY_AND_BARBER_SHOPS": {}, "BILLIARD_AND_POOL_ESTABLISHMENTS": {}, "BOAT_RENTAL_MARINAS_AND_MARINE_SERVICE_INC_SUPPLIES": {},
	"BOOKS_AND_STATIONARY": {}, "BUSINESS_AND_SECRETARIAL_SCHOOLS": {}, "BUYING_AND_SHOPPING_SERVICES_AND_CLUBS": {},
	"CABLE_AND_OTHER_PAY_TELEVISION_SERVICES": {}, "CAMERA_AND_PHOTOGRAPHIC_SUPPLY_SHOPS": {}, "CAR_AND_TRUCK_DEALERS_SALES_SERVICES_REPAIRS_PARTS_AND_LEASING": {},
	"CARPET_AND_UPHOLSTERY_CLEANING": {}, "CATERERS": {}, "CHARITABLE_AND_SOCIAL_SERVICE_ORGANIZATIONS": {},
	"CHILD_CARE_SERVICES": {}, "CHIROPRACTORS": {}, "CIGAR_SHOPS_AND_STANDS": {},
	"CIVIC_SOCIAL_AND_FRATERNAL_ASSOCIATIONS": {}, "CLEANING_MAINTENANCE_AND_JANITORIAL_SERVICES": {}, "CLOTHING_RENTALS_COSTUMES_UNIFORMS_AND_FORMAL_WEAR": {},
	"CLOTHING_STORES": {}, "COMMERCIAL_PHOTOGRAPHY_ART_AND_GRAPHICS": {}, "COMPUTER_NETWORKINFORMATION_SERVICES": {},
	"COMPUTER_SOFTWARE_OUTLETS": {}, "CONSUMER_CREDIT_REPORTING_AGENCIES": {}, "CONTRACTORS_STONEWORK_INSULATION_CAPENTRY_ROOFING_CONCRETE_OTHER": {},
	"CORRESPONDENCE_SCHOOLS": {}, "COSMETIC_SHOPS_EXISTING_BRAND": {}, "COSMETIC_SHOPS_OWN_BRAND": {},
	"COUNSELLING_SERVICES_DEBT_MARRIAGE_AND_PERSONAL": {}, "CRYPTOCURRENCY": {}, "DANCE_HALLS_STUDIOS_AND_SCHOOLS": {},
	"DATING_AND_ESCORT_SERVICES": {}, "DEALSITES_NON_DIGITAL_VOUCHER": {}, "DEALSITES_SELLING_EVOUCHER": {},
	"DENTAL_LABORATORY_MEDICAL_OPHTHALMIC_HOSPITAL_EQUIPMENT_AND_SUPPLIES": {}, "DENTISTS": {}, "DEPARTMENT_STORES": {},
	"DETECTIVE_AGENCIES_PROTECTIVE_AGENCIES_AND_SECURITY_SERVICES_INCLUDING_ARMOURED_CARS_AND_GUARD_DOGS": {}, "DIGITAL_GOODS": {}, "DIRECT_MARKETING_CATALOGUE_MERCHANTS": {},
	"DIRECT_MARKETING_COMBINATION_CATALOGUE_AND_RETAIL_MERCHANTS": {}, "DIRECT_MARKETING_CONTINUITYSUBSCRIPTION_MERCHANTS": {}, "DIRECT_MARKETING_INBOUND_TELEMARKETING_MERCHANTS": {},
	"DIRECT_MARKETING_INSURANCE_SERVICES": {}, "DIRECT_MARKETING_OUTBOUND_TELEMARKETING_MERCHANTS": {}, "DISCOUNT_SHOPS": {},
	"DOORTODOOR_SALES": {}, "DRAPERY_WINDOW_COVERING_UPHOLSTERY_FIREPLACES_FIREPLACE_SCREENS_AND_ACCESSORIES_SHOPS": {}, "DRINKING_PLACES_ALCOHOLIC_BEVERAGES_BARS_TAVERNS_NIGHTCLUBS_COCKTAIL_LOUNGES_AND_DISCOTHQUES": {},
	"DRUG_STORES_AND_PHARMACIES": {}, "DRUGS_DRUG_PROPRIETORS": {}, "DUTYFREE_SHOPS": {},
	"EATING_PLACES_AND_RESTAURANTS": {}, "ECOMMERCE": {}, "ELECTRIC_RAZOR_SHOPS_SALES_AND_SERVICE": {},
	"ELECTRIC_VEHICLE_CHARGING": {}, "ELECTRONICS_AND_ACCESSORIES_NEW": {}, "ELECTRONICS_REPAIR_SHOPS": {},
	"EMPLOYMENT_AGENCIES_AND_TEMPORARY_HELP_SERVICES": {}, "EQUIPMENT_TOOL_FURNITURE_AND_APPLIANCE_RENTALS_AND_LEASING": {}, "EWALLETS": {},
	"EXTERMINATING_AND_DISINFECTING_SERVICES": {}, "FAST_FOOD_RESTAURANTS": {}, "FINANCIAL_INSTITUTIONS_AUTOMATED_CASH_DISBURSEMENTS": {},
	"FINANCIAL_INSTITUTIONS_MANUAL_CASH_DISBURSEMENTS": {}, "FINANCIAL_INSTITUTIONS_MERCHANDISE_AND_SERVICES": {}, "FIREPLACES_FIREPLACE_SCREENS_AND_ACCESSORIES_SHOPS": {},
	"FLOOR_COVERING_SERVICES": {}, "FLORISTS": {}, "FLORISTS_SUPPLIES_NURSERY_STOCK_AND_FLOWERS": {},
	"FUEL_DEALERS_FUEL_OIL_WOOD_COAL_AND_LIQUEFIED_PETROLEUM": {}, "FUNERAL_SERVICES_AND_CREMATORIUMS": {}, "FURNITURE_HOME_FURNISHINGS_AND_EQUIPMENT_SHOPS_AND_MANUFACTURERS_EXCEPT_APPLIANCES": {},
	"GENERAL_CONTRACTORS_RESIDENTIAL_AND_COMMERCIAL": {}, "GIFT_CARD_NOVELTY_AND_SOUVENIR_SHOPS": {}, "GLASSWARE_AND_CRYSTAL_SHOPS": {},
	"HAJ_AND_UMRAH_PROVIDER_NEW": {}, "HEALTH_AND_BEAUTY_SPAS": {}, "HEALTH_SUPPLEMENT_SELLING_OWN_BRAND": {},
	"HEARING_AIDS_ORTHOPAEDIC_GOODS_AND_PROSTHETIC_DEVICES_SALES_SERVICE_AND_SUPPLIES": {}, "HEATING_PLUMBING_AND_AIRCONDITIONING_CONTRACTORS": {}, "HOBBY_TOY_AND_GAME_SHOPS": {},
	"HOME_SUPPLY_HARDWARE_AND_OTHER_BUILDING_MATERIALS": {}, "HOSPITALS": {}, "HOUSEHOLD_APPLICANCES_HOME_FURNISHING_AND_ELECTRONICS_SHOPS": {},
	"INSURANCE_SALES_UNDERWRITING_AND_PREMIUMS": {}, "INSURTECH": {}, "INTERNET_SERVICE_PROVIDER_NEW": {},
	"INVESTMENT_SERVICES_NEW": {}, "IT_SERVICES_NEW": {}, "JEWELLERY_WATCH_CLOCK_AND_SILVERWARE_SHOPS": {},
	"LANDSCAPING_AND_HORTICULTURAL_SERVICES": {}, "LAUNDRY_CLEANING_AND_GARMENT_SERVICES": {}, "LAWN_AND_GARDEN_SUPPLIES_OUTLETS_INCLUDING_NURSERIES": {},
	"LEGAL_SERVICES_AND_ATTORNEYS": {}, "LENDING": {}, "LOCAL_AND_SUBURBAN_COMMUTER_PASSENGER_TRANSPORTATION_INCLUDING_FERRIES": {},
	"LODGING_HOTELS_MOTELS_AND_RESORTS": {}, "LUGGAGE_AND_LEATHER_GOODS_SHOPS": {}, "MANAGEMENT_CONSULTING_AND_PUBLIC_RELATIONS_SERVICES": {},
	"MARKETPLACE_NEW": {}, "MASSAGE_PARLOURS": {}, "MEDICAL_AND_DENTAL_LABORATORIES": {},
	"MEMBERSHIP_CLUBS_SPORTS_RECREATION_ATHLETIC_COUNTRY_CLUBS_AND_PRIVATE_GOLF_COURSES": {}, "METAL_SERVICE_CENTRES_AND_OFFICES": {}, "MISCELLANEOUS_AND_SPECIALITY_RETAIL_OUTLETS": {},
	"MISCELLANEOUS_APPAREL_AND_ACCESSORY_SHOPS": {}, "MISCELLANEOUS_FOOD_SHOPS_CONVENIENCE_AND_SPECIALITY_RETAIL_OUTLETS": {}, "MISCELLANEOUS_GENERAL_MERCHANDISE": {},
	"MISCELLANEOUS_REPAIR_SHOPS_AND_RELATED_SERVICES": {}, "MOBILE_HOME_DEALERS": {}, "MONTHLY_SUMMARY_TELEPHONE_CHARGES": {},
	"MOTION_PICTURE_THEATRES": {}, "MOTOR_FREIGHT_CARRIERS_AND_TRUCKING_LOCAL_AND_LONG_DISTANCE_MOVING_AND_STORAGE_COMPANIES_AND_LOCAL_DELIVERY": {}, "MOTOR_VEHICLE_SUPPLIES_AND_NEW_PARTS": {},
	"MULTI_FINANCE_COMPANY": {}, "MULTI_LEVEL_MARKETING_NEW": {}, "MUSIC_SHOPS_MUSICAL_INSTRUMENTS_PIANOS_AND_SHEET_MUSIC": {},
	"NEWSAGENTS_AND_NEWSSTANDS": {}, "NON_FINANCIAL_INSTITUTIONS_FOREIGN_CURRENCY_MONEY_ORDERS_NOT_WIRE_TRANSFER_SCRIP_AND_TRAVELLERS_CHECKS": {}, "NURSING_AND_PERSONAL_CARE_FACILITIES": {},
	"OFFICE_AND_COMMERCIAL_FURNITURE": {}, "OFFICE_PHOTOGRAPHIC_COMMERCIAL_EQUIPMENT_AND_OTHER_INDUSTRIAL_SUPPLIES": {}, "OPTICIANS_OPTICAL_GOODS_AND_EYEGLASSES": {},
	"OPTOMETRISTS_AND_OPHTHALMOLOGISTS": {}, "OSTEOPATHS": {}, "OTHER_BUSINESS_SERVICES": {},
	"OTHER_CHEMICALS_AND_ALLIED_PRODUCTS": {}, "OTHER_CONSTRUCTION_MATERIALS": {}, "OTHER_DIRECT_MARKETINGDIRECT_MARKETERS": {},
	"OTHER_DOCTORS_AND_PHYSICIANS": {}, "OTHER_DURABLE_GOODS": {}, "OTHER_GOVERNMENT_SERVICES": {},
	"OTHER_MEDICAL_SERVICES_AND_HEALTH_PRACTITIONERS": {}, "OTHER_MEMBERSHIP_ORGANIZATIONS": {}, "OTHER_MISCELLANEOUS_PERSONAL_SERVICES": {},
	"OTHER_NONDURABLE_GOODS": {}, "OTHER_PROFESSIONAL_SERVICES": {}, "OTHER_RECREATION_SERVICES": {},
	"OTHER_TRANSPORTATION_SERVICES": {}, "PACKAGE_SHOPS_BEER_WINE_AND_LIQUOR": {}, "PARKING_LOTS_AND_GARAGES": {},
	"PAWN_SHOPS": {}, "PAYMENT_GATEWAY_NEW": {}, "PEERTOPEER_LENDING": {},
	"PET_SHOPS_PET_FOOD_AND_SUPPLIES": {}, "PETROLEUM_AND_PETROLEUM_PRODUCTS": {}, "PHOTOFINISHING_LABORATORIES_AND_PHOTO_DEVELOPING": {},
	"PHOTOGRAPHIC_STUDIOS": {}, "PIECE_GOODS_NOTIONS_AND_OTHER_DRY_GOODS": {}, "PLUMBING_AND_HEATING_EQUIPMENT_AND_SUPPLIES": {},
	"PODIATRISTS_AND_CHIROPODISTS": {}, "POLITICAL_ORGANIZATIONS": {}, "PRECIOUS_STONES_AND_METALS_WATCHES_AND_JEWELLERY": {},
	"PUBLIC_WAREHOUSING_AND_STORAGE_FARM_PRODUCTS_REFRIGERATED_GOODS_AND_HOUSEHOLD_GOODS": {}, "REAL_ESTATE_AGENTS_AND_MANAGERS": {}, "RECORD_SHOPS": {},
	"RELIGIOUS_GOODS_AND_SHOPS": {}, "RELIGIOUS_ORGANIZATIONS": {}, "REMITTANCE_NEW": {},
	"RIDE_SHARING_NEW": {}, "SCHOOLS_UNIVERSITY_AND_EDUCATIONAL_SERVICE_INC_TRADE_AND_VOCATIONAL_SCHOOL": {}, "SECURITIES_BROKERS_AND_DEALERS": {},
	"SERVICE_STATIONS_WITH_OR_WITHOUT_ANCILLARY_SERVICES": {}, "SEWING_NEEDLEWORK_FABRIC_AND_PIECE_GOODS_SHOPS": {}, "SHOE_REPAIR_SHOPS_SHOE_SHINE_PARLOURS_AND_HAT_CLEANING_SHOPS": {},
	"SHOE_SHOPS": {}, "SPECIALITY_CLEANING_POLISHING_AND_SANITATION_PREPARATIONS": {}, "SPORTING_AND_RECREATIONAL_CAMPS": {},
	"SPORTING_GOODS_NEW": {}, "SPORTS_AND_RIDING_APPAREL_SHOPS": {}, "SUPERMARKET_CONVENIENCE_STORES_CONFECTIONERY_SHOPS_AND_BUTCHERY": {},
	"SWIMMING_POOLS_SALES_SUPPLIES_AND_SERVICES": {}, "TAILORS_SEAMSTRESSES_MENDING_AND_ALTERATIONS": {}, "TAX_PAYMENTS": {},
	"TAX_PREPARATION_SERVICES": {}, "TAXICABS_AND_LIMOUSINES": {}, "TELECOMMUNICATION_EQUIPMENT_AND_TELEPHONE_SALES": {},
	"TELECOMMUNICATION_SERVICES_INCLUDING_LOCAL_AND_LONG_DISTANCE_CALLS_CREDIT_CARD_CALLS_CALLS_THROUGH_USE_OF_MAGNETIC_STRIPE_READING_TELEPHONES_AND_FAXES": {}, "TELEGRAPH_SERVICES": {}, "TELEMARKETING_TRAVEL_RELATED_ARRANGEMENT_SERVICES": {},
	"TENT_AND_AWNING_SHOPS": {}, "TESTING_LABORATORIES_NONMEDICAL": {}, "THEATRICAL_PRODUCERS_EXCEPT_MOTION_PICTURES_AND_TICKET_AGENCIES": {},
	"TIMESHARES": {}, "TOLLS_AND_BRIDGE_FEES": {}, "TOURIST_ATTRACTIONS_AND_EXHIBITS": {},
	"TRAILER_PARKS_AND_CAMPSITES": {}, "TRAINING_ONLINE_INCLASS_SHORT_COURSES": {}, "TRAVEL_AGENCIES_AND_TOUR_OPERATORS": {},
	"TRUCK_AND_UTILITY_TRAILER_RENTALS": {}, "TYPEWRITER_OUTLETS_SALES_SERVICE_AND_RENTALS": {}, "UNIFORM_AND_COMMERCIAL_CLOTHING": {},
	"UTILITIES_ELECTRIC_GAS_WATER_AND_SANITARY": {}, "VETERINARY_SERVICES": {}, "VIDEO_AMUSEMENT_GAME_SUPPLIES": {},
	"VIDEO_GAME_ARCADES_AND_ESTABLISHMENTS": {}, "VIDEO_TAPE_RENTALS": {}, "WELDING_SERVICES": {},
	"WIRE_TRANSFERS_AND_MONEY_ORDERS": {}, "WRECKING_AND_SALVAGE_YARDS": {}, "ZAKAT_COLLECTION_NEW": {},
}

// IsValidIndustryCategory reports whether s is an accepted Xendit industry_category value.
func IsValidIndustryCategory(s string) bool {
	_, ok := validIndustryCategories[s]
	return ok
}

var validBusinessTypes = map[string]struct{}{
	"CORPORATION":         {},
	"PARTNERSHIP":         {},
	"SOLE_PROPRIETORSHIP": {},
	"INDIVIDUAL":          {},
	"FOREIGN":             {},
	"FOREIGN_SEC":         {},
	"FOREIGN_NONSEC":      {},
}

// IsValidBusinessType reports whether s is an accepted Xendit business entity type.
func IsValidBusinessType(s string) bool {
	_, ok := validBusinessTypes[s]
	return ok
}

// AccountHolderBusinessDetail is the KYC business information for CreateAccountHolder.
type AccountHolderBusinessDetail struct {
	Type               string `json:"type"                          example:"CORPORATION"` // CORPORATION | PARTNERSHIP | SOLE_PROPRIETORSHIP | INDIVIDUAL
	LegalName          string `json:"legal_name"                    example:"PT Toko ABC"`
	IndustryCategory   string `json:"industry_category"             example:"RETAIL"`
	CountryOfOperation string `json:"country_of_operation"          example:"ID"`
	TradingName        string `json:"trading_name,omitempty"        example:"Toko ABC"`
	Description        string `json:"description,omitempty"         example:"Online fashion store"`
	DateOfRegistration string `json:"date_of_registration,omitempty" example:"2020-01-15"` // YYYY-MM-DD
}

// AccountHolderAddress is the registered address for CreateAccountHolder.
type AccountHolderAddress struct {
	Country       string `json:"country"                  example:"ID"`
	City          string `json:"city"                     example:"Jakarta"`
	StreetLine1   string `json:"street_line1"             example:"Jl. Sudirman No. 1"`
	PostalCode    string `json:"postal_code"              example:"12190"`
	ProvinceState string `json:"province_state,omitempty" example:"DKI Jakarta"`
}

// AccountHolderIndividualDetail represents one person (PIC or Incorporator) for CreateAccountHolder.
type AccountHolderIndividualDetail struct {
	Type         string `json:"type"                    example:"PIC"`   // PIC | Incorporator
	Role         string `json:"role"                    example:"Owner"` // e.g. Owner, Director
	GivenNames   string `json:"given_names"             example:"Budi"`
	Surname      string `json:"surname"                 example:"Santoso"`
	PhoneNumber  string `json:"phone_number"            example:"+6281234567890"`
	Email        string `json:"email"                   example:"budi@example.com"`
	Nationality  string `json:"nationality"             example:"ID"` // ISO 3166-2
	PlaceOfBirth string `json:"place_of_birth,omitempty" example:"Jakarta"`
	DateOfBirth  string `json:"date_of_birth,omitempty"  example:"1990-01-15"` // YYYY-MM-DD
	Gender       string `json:"gender,omitempty"         example:"MALE"`       // MALE | FEMALE | OTHER
}

// AccountHolderKYCDocument is one document entry in the kyc_documents array.
// file_id must be obtained by uploading the document via Xendit's Upload File API first.
type AccountHolderKYCDocument struct {
	Country   string `json:"country"              example:"ID"`
	Type      string `json:"type"                 example:"ID_NIB"` // e.g. ID_NIB, ID_COMPANY_NPWP, ID_AKTA, ID_NATIONAL_ID_KTP
	FileID    string `json:"file_id"              example:"<file_id from Xendit Upload API>"`
	ExpiresAt string `json:"expires_at,omitempty" example:"2030-12-31"` // YYYY-MM-DD
}

// CreateAccountHolderRequest is the input for CreateAccountHolder.
// Defined here (not in pkg/xendit) so the interface can reference it without circular imports.
type CreateAccountHolderRequest struct {
	BusinessDetail    AccountHolderBusinessDetail     `json:"business_detail"`
	IndividualDetails []AccountHolderIndividualDetail `json:"individual_details"`
	KYCDocuments      []AccountHolderKYCDocument      `json:"kyc_documents"`
	Address           AccountHolderAddress            `json:"address"`
	Email             string                          `json:"email"                 example:"merchant@example.com"`
	PhoneNumber       string                          `json:"phone_number"          example:"+6281234567890"`
	WebsiteURL        string                          `json:"website_url,omitempty" example:"https://tokoabc.com"`
}

// XenditAccountInfo holds the live account status returned by Xendit's GET /v2/accounts/{id}.
// Defined here (not in pkg/xendit) so pkg/xendit can import internal/account without
// introducing a circular dependency (internal/account does not import pkg/xendit).
type XenditAccountInfo struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Email         string `json:"email"`
	Status        string `json:"status"`
	PublicProfile struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"public_profile"`
}

// ─── Request body types (used by Swagger) ────────────────────────────────────

// TopupBody is the request body for POST /accounts/balance/topup.
type TopupBody struct {
	Amount   int64  `json:"amount" example:"500000"`
	Currency string `json:"currency" example:"IDR"`
	Note     string `json:"note" example:"Top up for merchant A"`
}

// CreateHoldBody is the request body for POST /accounts/holds.
type CreateHoldBody struct {
	OrderNumber string `json:"order_number" example:"ORD-001"`
	Amount      int64  `json:"amount" example:"35000"`
	Currency    string `json:"currency" example:"IDR"`
}

// CreateGatewaySubAccountBody is the request body for POST /accounts/gateway/sub-account.
type CreateGatewaySubAccountBody struct {
	Email string `json:"email" example:"toko-abc@example.com"`
	Name  string `json:"name" example:"Toko ABC"`
}

// SendGatewayPayoutBody is the request body for POST /accounts/gateway/payout.
type SendGatewayPayoutBody struct {
	Amount            int64  `json:"amount" example:"100000"`
	InvoiceNumber     string `json:"invoice_number" example:"INV/2026/001"`
	BankCode          string `json:"bank_code" example:"BNINIDJA"`
	BankAccountNumber string `json:"bank_account_number" example:"0123456789"`
	BankAccountName   string `json:"bank_account_name" example:"Budi Santoso"`
}
