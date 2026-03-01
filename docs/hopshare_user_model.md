# Hopshare Member/Organization Model

## Signing Up

Members who sign up to Hopshare must create an account. Each Member account must contain the following information

* first name
* last name
* password (encrypted)
* email address
* preferred contact (free text string)

In addition, a Member's profile can optionally contain
* profile picture (to be used as an avatar)
* location (city, state)

## Initial Login

After logging into Hopshare the first time, a Member needs to be included in at least one Organization. They can accomplish this in one of two ways:

* Create their own Organization. This makes them the primary Owner in the Organization. They can invite other Members to also be Owners in the Organization, but only they are the primary Owner. 
* Search for an Organization (all Organization names are publicly visible) and request to be added as a Member.

This is a necessary first step before a Member can use Hopshare- they must be associated with an Organization.

## Managing Organization Membership

At any time, a Member can request membership in another Organization. They can be part of up to five Organizations simultaneously. Requests for membership to to the Organization Owners (via email) and any Owner can approve or reject the membership request.

Additionally, a Member can leave an Organization at any time. This deletes their account in that Organization's time bank forever. If they join the Organization again they start over with a new profile. Members who are part of a single Organization may not leave it however, as that would contradict the rule that all Members must be part of at least one Organization. Instead, Members who wish to leave their one Organization must delete their Hopshare account entirely.

## Organization Management

Members of an Organization may be invited to become Owners. Owners have special access to control the Organization. Only one Owner of an Organization is designated as the primary Owner. Only the primary Owner (or an Administrator) can delete the Organization. The primary Owner (or Administrator) can also transfer the role of primary Owner to another Owner if that Member is not already a primary Owner of an Organization.

Owners can invite and remove other non-Owner Members from the Organization. Only the primary Owner can "downgrade" other Owners from the Organization back to regular Members.

## Administrators

There is a very special class of Members that are designated as Administrators. These Members have superpowers on the application. An Administrator has abilities that cross Organizations and operate the application, resolve disputes, and assist Members with issues they cannot otherwise perform. It is a special configuration task to create and manage Administrators- this is not a function regular Members have access to.

## Organizations

An Organization is a logical collection of Members (some of whom are Owners) that represent a 'tenant' in Hopshare. Each Organization when created must have:

* A unique name
* A logo (which may be system supplied)

Each Organization is associated with the Member who created it (the primary Owner). It may have multiple Owners associated with it as well.

Each Organization has its own set of Accounts for each Member and Owner, and a policy that specifies how the Timebank operates. There is only one Timebank per Organization.
