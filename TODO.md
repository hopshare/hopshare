# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7

## Now

* There is no easy way to see what Hops I have offered to help with...should I get a message?
    it is possible to offer help multiple times!! n
* Refactor service.go into separate files by concept- orgs/members, hops, messages
* Refactor 'logout' tab on header to be a User avatar with pull down menu to go to Profile or Logout
    * Create a user Profile page where users can change password, upload photo, manage Skills, etc...
* Add location to Organization- that can be searched by.
* Organization "Wall"- closest thing to 'social media' feature- inspire others.
* Make a photo mandatory for closing a request (Simon's idea)? Organization Album concept?
* Owners are moderators for listings- they can flag/delete inappropriate requests/comments
* Organizations need to have a readable URL for new joiners. A way for users and non-users to sign up quickly.
* Skills profiles for users? We will need something for automatic matching...give it some thought. Skills should reside in the database- we can seed some starter ones, but it should grow over time- and be scoped within the organization. We can have these configured for new joiners via a wizard interface.
* Administrator page- see everything, do dangerous stuff. Link conditionally off header menu for Admin users.


We need to track offers of help for Hops in the database. Create a table that maps Member ids against Hop ids, storing the date and time when an offer to help is made, and a status of 'accepted' or 'denied' (with appropriate timestamps) based on which Member is chosen for that Hop. Populate this table when offers to help are made, or accepted/denied by Members. Also, use this table when a Member is looking at the My Hops page- it should include Hops where they have offered to help (use a new badge labeled 'Help Offered') but have not yet received an accept/denied response.

## Later

* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

## Bugs

* An Organization Owner can request membership in their own Organization- this should be prevented
* "Your Activity" should fetch new every time it opens- otherwise you get stale Hop statuses.
* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.

