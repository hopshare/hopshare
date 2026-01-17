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

* Remember the last Organization I was in (set current organization in User table?)
* Add a new state- confirmation of help- after an offer to help. What if multiple users offer to help? Accepted requests need to be confirmed by the person raising the request. Or time out.
* Also- ask for more details- before Accepting? Like FB Marketplace.
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


## Later

* We should add some mocked email service- or an in-app messaging facility so that Members can communicate around a Request.
* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

## Bugs

* An Organization Owner can request membership in their own Organization- this should be prevented

